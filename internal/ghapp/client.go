package ghapp

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxRetries     = 3
	installPerPage = 100
)

var defaultHTTP = &http.Client{Timeout: 30 * time.Second}

type Client struct {
	BaseURL    string
	HTTP       *http.Client
	Now        func() time.Time
	RetryDelay time.Duration
	ClientID   string
	Key        *rsa.PrivateKey
}

type Credential struct {
	Token     string
	Slug      string
	ExpiresAt time.Time
}

type Installation struct {
	ID    int64
	Login string
}

type request struct {
	op       string
	method   string
	path     string
	bearer   string
	viaJWT   bool
	notFound string
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func (c *Client) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP == nil {
		return defaultHTTP
	}
	return c.HTTP
}

func (c *Client) newRequest(ctx context.Context, r request) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, r.method, strings.TrimRight(c.BaseURL, "/")+r.path, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot create request: %w", r.op, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "botid")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+r.bearer)
	return req, nil
}

func (c *Client) roundTrip(req *http.Request) (*response, error) {
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &response{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

// send retries only 5xx responses and network errors, up to maxRetries times.
// A 5xx that is still failing after the retries are used up is returned as a response so the caller can handle it as a status.
func (c *Client) send(ctx context.Context, r request) (*response, error) {
	req, err := c.newRequest(ctx, r)
	if err != nil {
		return nil, err
	}
	for attempt := 0; ; attempt++ {
		resp, err := c.roundTrip(req)
		if err == nil && resp.status < 500 {
			return resp, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("%s: %w", r.op, ctxErr)
		}
		if attempt == maxRetries {
			if err != nil {
				return nil, fmt.Errorf("%s: cannot connect to GitHub API (after %d retries): %w", r.op, maxRetries, err)
			}
			return resp, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%s: %w", r.op, ctx.Err())
		case <-time.After(c.RetryDelay):
		}
	}
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	resp, err := c.send(ctx, r)
	if err != nil {
		return err
	}
	if resp.status < 200 || resp.status >= 300 {
		return c.statusError(r, resp)
	}
	if err := json.Unmarshal(resp.body, out); err != nil {
		return fmt.Errorf("%s: cannot read GitHub response: %w", r.op, err)
	}
	return nil
}

func (c *Client) statusError(r request, resp *response) error {
	msg := apiMessage(resp.body)
	var text string
	switch {
	case resp.status == http.StatusUnauthorized && r.viaJWT:
		text = "authentication failed (401); the Client ID or private key may be wrong, or the clock on this PC may be off"
	case resp.status == http.StatusUnauthorized:
		text = "installation token is invalid or expired (401)"
	case resp.status == http.StatusNotFound:
		text = "not found (404)"
		if r.notFound != "" {
			text += "; " + r.notFound
		}
	case resp.status == http.StatusForbidden && strings.Contains(msg, "Resource not accessible by integration"):
		text = "the App does not have enough permissions (403); check the GitHub App permission settings"
	case resp.status == http.StatusTooManyRequests || (resp.status == http.StatusForbidden && rateLimited(resp.header, msg)):
		text = fmt.Sprintf("rate limit reached (%d); %s; not retrying automatically", resp.status, c.rateLimitWait(resp.header))
	case resp.status == http.StatusForbidden:
		text = "access denied (403)"
	case resp.status >= 500:
		text = fmt.Sprintf("GitHub API returned a server error (%d); it did not recover after %d retries", resp.status, maxRetries)
	default:
		text = fmt.Sprintf("unexpected status (%d) returned", resp.status)
	}
	if msg != "" {
		text += fmt.Sprintf(" [GitHub: %s]", msg)
	}
	return fmt.Errorf("%s: %s", r.op, text)
}

func rateLimited(h http.Header, msg string) bool {
	return h.Get("X-Ratelimit-Remaining") == "0" ||
		h.Get("Retry-After") != "" ||
		strings.Contains(strings.ToLower(msg), "rate limit")
}

func (c *Client) rateLimitWait(h http.Header) string {
	if reset := h.Get("X-Ratelimit-Reset"); reset != "" {
		if sec, err := strconv.ParseInt(reset, 10, 64); err == nil {
			wait := time.Unix(sec, 0).Sub(c.now())
			return fmt.Sprintf("you can run again in about %d seconds (x-ratelimit-reset)", max(int64(wait.Seconds()), 0))
		}
	}
	if after := h.Get("Retry-After"); after != "" {
		if sec, err := strconv.ParseInt(after, 10, 64); err == nil {
			return fmt.Sprintf("you can run again in about %d seconds (retry-after)", sec)
		}
	}
	return "could not determine how long to wait before running again"
}

func (c *Client) Installations(ctx context.Context) ([]Installation, error) {
	jwt, err := c.jwt()
	if err != nil {
		return nil, err
	}

	type installationJSON struct {
		ID          *int64  `json:"id"`
		SuspendedAt *string `json:"suspended_at"`
		Account     *struct {
			Login string `json:"login"`
		} `json:"account"`
	}

	installations := []Installation{}
	for page := 1; ; page++ {
		var items []installationJSON
		err := c.do(ctx, request{
			op:     "listing installations",
			method: http.MethodGet,
			path:   fmt.Sprintf("/app/installations?per_page=%d&page=%d", installPerPage, page),
			bearer: jwt,
			viaJWT: true,
		}, &items)
		if err != nil {
			return nil, err
		}
		for i, it := range items {
			if it.SuspendedAt != nil {
				continue
			}
			if it.ID == nil || *it.ID == 0 {
				return nil, fmt.Errorf("listing installations: page %d, item %d has no id", page, i+1)
			}
			if it.Account == nil || it.Account.Login == "" {
				return nil, fmt.Errorf("listing installations: installation %d has no account.login", *it.ID)
			}
			installations = append(installations, Installation{ID: *it.ID, Login: it.Account.Login})
		}
		if len(items) < installPerPage {
			return installations, nil
		}
	}
}

func (c *Client) Issue(ctx context.Context, installationID int64) (Credential, error) {
	if installationID <= 0 {
		return Credential{}, errors.New("installationId is invalid; run botid init")
	}
	jwt, err := c.jwt()
	if err != nil {
		return Credential{}, err
	}

	var tok struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	err = c.do(ctx, request{
		op:       "issuing access token",
		method:   http.MethodPost,
		path:     fmt.Sprintf("/app/installations/%d/access_tokens", installationID),
		bearer:   jwt,
		viaJWT:   true,
		notFound: "the installation may have been deleted; run botid init again and choose an installation",
	}, &tok)
	if err != nil {
		return Credential{}, err
	}
	if tok.Token == "" {
		return Credential{}, errors.New("issuing access token: response has no token")
	}
	expiresAt, err := time.Parse(time.RFC3339, tok.ExpiresAt)
	if err != nil {
		return Credential{}, fmt.Errorf("issuing access token: cannot read expires_at: %w", err)
	}

	var app struct {
		Slug string `json:"slug"`
	}
	err = c.do(ctx, request{
		op:     "getting App info",
		method: http.MethodGet,
		path:   "/app",
		bearer: jwt,
		viaJWT: true,
	}, &app)
	if err != nil {
		return Credential{}, err
	}
	if app.Slug == "" {
		return Credential{}, errors.New("getting App info: response has no slug")
	}

	return Credential{Token: tok.Token, Slug: app.Slug, ExpiresAt: expiresAt}, nil
}

func (c *Client) BotUserID(ctx context.Context, token, slug string) (int64, error) {
	if token == "" {
		return 0, errors.New("getting bot user ID: token is empty")
	}
	if slug == "" {
		return 0, errors.New("getting bot user ID: slug is empty")
	}

	var user struct {
		ID *int64 `json:"id"`
	}
	err := c.do(ctx, request{
		op:     "getting bot user ID",
		method: http.MethodGet,
		path:   "/users/" + url.PathEscape(slug) + "%5Bbot%5D",
		bearer: token,
	}, &user)
	if err != nil {
		return 0, err
	}
	if user.ID == nil || *user.ID == 0 {
		return 0, errors.New("getting bot user ID: response has no id")
	}
	return *user.ID, nil
}

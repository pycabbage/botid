import { Plugin } from "@opencode/plugin"

export default Plugin.define({
  id: "botid.env",
  async setup(ctx) {
    await ctx.shell.hook("create.before", (event) => {
      event.env.GH_CONFIG_DIR = `${process.env.HOME}/.local/share/botid/gh`
      event.env.GIT_CONFIG_GLOBAL = `${process.env.HOME}/.local/share/botid/git/config`
    })
  }
})

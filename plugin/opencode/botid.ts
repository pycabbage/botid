import { execFile } from "node:child_process"
import { promisify } from "node:util"
import { Plugin } from "@opencode/plugin"

export default Plugin.define({
  id: "botid.env",
  async setup(ctx) {
    await ctx.shell.hook("create.before", async (event) => {
      Object.assign(
        event.env,
        JSON.parse(
          (await promisify(execFile)("botid", ["env", "--format", "json", "--cwd", event.cwd], { cwd: ctx.location.directory })).stdout
        )
      )
    })
  }
})

import { spawn } from "node:child_process"
import { writeFile, rm } from "node:fs/promises"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import { Plugin } from "@opencode/plugin"

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const bin = join(root, "bin", process.platform === "win32" ? "collector.exe" : "collector")

const directories = new Map<string, string>()
const lastStop = new Map<string, number>()

type EventRecord = {
  type?: string
  properties?: Record<string, unknown>
  data?: Record<string, unknown>
}

function fields(event: EventRecord): Record<string, unknown> {
  return event.properties ?? event.data ?? {}
}

function text(value: unknown): string {
  return typeof value === "string" ? value : ""
}

function sessionID(event: EventRecord): string {
  const props = fields(event)
  return text(props.sessionID) || text(props.session_id)
}

function directoryOf(props: Record<string, unknown>): string {
  if (typeof props.directory === "string") return props.directory
  const location = props.location
  if (typeof location === "string") return location
  if (location && typeof location === "object") {
    const directory = (location as { directory?: unknown }).directory
    if (typeof directory === "string") return directory
  }
  return ""
}

function modelName(props: Record<string, unknown>): string {
  const model = props.model
  if (typeof model === "string") return model
  if (model && typeof model === "object") {
    const record = model as { providerID?: unknown; id?: unknown }
    return [record.providerID, record.id].filter((part) => typeof part === "string").join("/")
  }
  return ""
}

function idle(event: EventRecord): boolean {
  if (event.type === "session.idle") return true
  if (event.type !== "session.status") return false
  const status = fields(event).status
  if (status === "idle") return true
  return !!status && typeof status === "object" && (status as { type?: unknown }).type === "idle"
}

function deliver(body: Record<string, string>, transcript: string) {
  const child = spawn(bin, ["opencode-hook"], { stdio: ["pipe", "ignore", "ignore"] })
  child.stdin?.end(JSON.stringify(body))
  child.once("close", () => {
    if (transcript) void rm(transcript, { force: true })
  })
}

async function transcriptPath(ctx: { session?: { context?: (input: { sessionID: string }) => Promise<unknown> } }, session: string) {
  if (!session || !ctx.session?.context) return ""
  try {
    const result = await ctx.session.context({ sessionID: session })
    const list = Array.isArray(result) ? result : (result as { messages?: unknown }).messages
    if (!Array.isArray(list) || list.length === 0) return ""
    const file = join(tmpdir(), `opencode-${session}-${Date.now()}.jsonl`)
    await writeFile(file, list.map((message) => JSON.stringify(message)).join("\n") + "\n")
    return file
  } catch {
    return ""
  }
}

export default Plugin.define({
  id: "opencode-collector",
  setup(ctx) {
    const fallback = ctx.location?.project?.directory || ctx.location?.directory || ""
    const controller = new AbortController()
    void (async () => {
      for await (const event of ctx.event.subscribe({
        signal: controller.signal,
        types: ["session.created", "session.idle", "session.deleted", "session.status"],
      })) {
        const current = event as EventRecord
        const props = fields(current)
        const session = sessionID(current)
        const directory = directoryOf(props) || directories.get(session) || fallback
        if (session && directory) directories.set(session, directory)
        let hook = ""
        if (current.type === "session.created") hook = "SessionStart"
        else if (current.type === "session.deleted") hook = "SessionEnd"
        else if (idle(current)) {
          const now = Date.now()
          if (now - (lastStop.get(session) ?? 0) < 2000) continue
          lastStop.set(session, now)
          hook = "Stop"
        }
        if (!hook || !directory) continue
        const transcript = hook === "Stop" ? await transcriptPath(ctx, session) : ""
        deliver(
          {
            hook_event_name: hook,
            session_id: session,
            cwd: directory,
            transcript_path: transcript,
            model: modelName(props),
            host: "opencode",
          },
          transcript,
        )
        if (current.type === "session.deleted") directories.delete(session)
      }
    })()
    return () => controller.abort()
  },
})

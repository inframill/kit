import { spawn, execFile, type ChildProcess } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { promisify } from "node:util";
import { Registry } from "./registry.js";
import { inspect } from "node:util";
import { setTimeout as delay } from "node:timers/promises";
import { createClient, type Client } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import { EngineService, CommandStatus, type CommandExecution } from "./gen/inframill/v1/inframill_pb.js";

export async function poll<T>(description: string, check: () => Promise<T | undefined>, timeout = 8000): Promise<T> {
  const deadline = Date.now() + timeout;
  do {
    const result = await check();
    if (result !== undefined) return result;
    await delay(25);
  } while (Date.now() < deadline);
  throw new Error(`Timed out: ${description}`);
}

async function freePort(): Promise<number> {
  const listener = createServer();
  await new Promise<void>((ok, fail) => {
    listener.once("error", fail);
    listener.listen(0, "127.0.0.1", ok);
  });
  const address = listener.address();
  if (!address || typeof address === "string") throw new Error("No test port assigned");
  await new Promise<void>((ok, fail) => listener.close(error => error ? fail(error) : ok()));
  return address.port;
}

export class Daemon {
  client!: Client<typeof EngineService>;
  private child?: ChildProcess;
  private exited?: Promise<void>;
  private diagnostics = "";
  private registry?: Registry;
  private readonly instanceNames = new Map<string, string>();
  reference = "";
  pinnedReference = "";

  instance(mode = "success") { return { blueprint: this.reference, values: { mode, name: "test" } }; }

  private constructor(readonly root: string, private readonly binary: string) {}

  static async create(binary = process.env.INFRAMILL_TEST_BINARY): Promise<Daemon> {
    if (!binary) throw new Error("INFRAMILL_TEST_BINARY is required; run task check-full");
    const daemon = new Daemon(await mkdtemp(join(tmpdir(), "inframill-jest-")), resolve(__dirname, "../../..", binary));
    try {
      const blueprint = join(daemon.root, "blueprints/fixture");
      await mkdir(join(blueprint, "template/debug"), { recursive: true });
      await mkdir(join(daemon.root, "bin"));
      await writeFile(join(blueprint, "manifest.yaml"), `api_version: inframill.dev/blueprint/v1
description: Jest fixture
drivers:
  debug: {}
`);
      await writeFile(join(blueprint, "schema.json"), JSON.stringify({
        type: "object", required: ["mode", "name"], additionalProperties: false,
        properties: { mode: { enum: ["success", "fail", "block"] }, name: { type: "string" } },
      }));
      await writeFile(join(blueprint, "template/debug/main.txt.gotmpl"), "# {{ .name }}\n");
      const builder = process.env.INFRAMILL_TEST_PACKAGE_BUILDER;
      if (!builder) throw new Error("INFRAMILL_TEST_PACKAGE_BUILDER is required; run task check-full");
      await promisify(execFile)(resolve(__dirname, "../../..", builder), [blueprint, join(daemon.root, "layout")]);
      daemon.registry = await Registry.create(join(daemon.root, "layout"));
      daemon.reference = daemon.registry.reference;
      await daemon.start();
      const fetched = await daemon.client.fetchBlueprint({ reference: daemon.reference });
      daemon.pinnedReference = fetched.blueprint!.reference;
      return daemon;
    } catch (error) {
      await daemon.dispose();
      throw error;
    }
  }

  async start(): Promise<void> {
    if (this.child) throw new Error("Daemon already started");
    const port = await freePort();
    this.diagnostics = "";
    const configPath = join(this.root, ".inframill.yaml");
    await writeFile(configPath, `
main:
  port: ${port}
logger:
  type: slog
  settings: { level: info, format: json }
handlers:
  - type: connect-engine
    settings:
      path: /inframill.v1.EngineService/
engine:
  drivers: {debug: {path: ${JSON.stringify(this.binary)}, args: [driver, debug]}}
workers: []
`);
    const child = spawn(this.binary, ["server", "--config", configPath], {
      cwd: this.root,
      // Do not inherit credentials or real infrastructure executables.
      env: { PATH: join(this.root, "bin"), HOME: this.root, TMPDIR: this.root,
        INFRAMILL_HTTP_REGISTRIES: this.registry?.host ?? "" },
      stdio: ["ignore", "pipe", "pipe"],
      detached: process.platform !== "win32",
    });
    this.child = child;
    this.exited = new Promise<void>(done => {
      child.once("close", () => done());
      child.once("error", error => { this.diagnostics += error.message; done(); });
    });
    for (const stream of [child.stdout, child.stderr]) {
      stream?.on("data", data => { this.diagnostics = (this.diagnostics + String(data)).slice(-8192); });
    }
    this.client = createClient(EngineService, createConnectTransport({
      baseUrl: `http://127.0.0.1:${port}`, httpVersion: "1.1",
      useBinaryFormat: false, defaultTimeoutMs: 1000,
    }));
    let readinessError: unknown;
    try {
      await poll("daemon readiness", async () => {
        if (child.exitCode !== null || child.signalCode !== null || !child.pid) {
          throw new Error(`Daemon startup failed: ${this.diagnostics}`);
        }
        try { await this.client.listCommandDefinitions({}); return true; }
        catch (error) {
          readinessError = error;
          return undefined;
        }
      });
    } catch (error) {
      await this.stop();
      throw new Error(`${String(error)}\n${String(readinessError ?? "")}\n${this.diagnostics}`);
    }
  }

  async stop(signal: "SIGTERM" | "SIGKILL" = "SIGTERM"): Promise<void> {
    const child = this.child;
    if (!child) return;
    const kill = (signal: "SIGTERM" | "SIGKILL") => {
      if (!child.pid || child.exitCode !== null || child.signalCode !== null) return;
      // Crash the whole disposable process group, excluding separately grouped Driver invocations.
      if (signal === "SIGKILL" && process.platform !== "win32") process.kill(-child.pid, signal);
      else child.kill(signal);
    };
    kill(signal);
    const wait = async (timeout: number) => {
      const controller = new AbortController();
      try {
        return await Promise.race([
          this.exited!.then(() => true),
          delay(timeout, false, { signal: controller.signal }),
        ]);
      } finally { controller.abort(); }
    };
    if (!await wait(3000)) {
      kill("SIGKILL");
      if (!await wait(3000)) throw new Error("Daemon would not terminate");
    }
    this.child = undefined;
  }

  async dispose(): Promise<void> {
    try { await this.stop(); await this.registry?.close(); }
    finally {
      for (const id of this.instanceNames.keys()) {
        try { await writeFile(join(this.root,".inframill/instances",id,"workspace/debug/release"),"release"); }
        catch (error) { if ((error as NodeJS.ErrnoException).code!=="ENOENT") throw error; }
      }
      // Give any Driver surviving a deliberate daemon crash time to leave its barrier.
      await delay(50);
      await rm(this.root, { recursive: true, force: true });
    }
  }

  async command(instanceId: string, id: string, status: CommandStatus): Promise<CommandExecution> {
    return poll(`command ${id} -> ${CommandStatus[status]}`, async () => {
      const record = (await this.client.getCommandExecution({ instanceId, commandId: id })).execution!;
      if (record.status === status) return record;
      if ([CommandStatus.SUCCEEDED, CommandStatus.FAILED, CommandStatus.INTERRUPTED].includes(record.status)) {
        throw new Error(`Unexpected terminal command: ${inspect(record)}`);
      }
      return undefined;
    });
  }

  async init(name: string, mode = "success"): Promise<string> {
    const info = (await this.client.initInstance({ name, instance: this.instance(mode) })).instance!;
    this.instanceNames.set(info.id, info.name);
    return info.id;
  }

  instancePath(instanceId: string): string {
    const name = this.instanceNames.get(instanceId);
    if (!name) throw new Error(`Unknown fixture Instance ${instanceId}`);
    return join(this.root, ".inframill", "instances", instanceId);
  }

  log(instanceId: string, command: string, stream: "stdout" | "stderr"): Promise<string> {
    return readFile(join(this.instancePath(instanceId), "logs", command, `${stream}.log`), "utf8");
  }

  async waiting(instanceId: string, command: string): Promise<void> {
    await poll("debug Driver barrier", async () => {
      try { return (await this.log(instanceId, command, "stderr")).includes("waiting for release") ? true : undefined; }
      catch (error) { if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error; return undefined; }
    });
  }

  release(instanceId: string): Promise<void> {
    return writeFile(join(this.instancePath(instanceId), "workspace/debug/release"), "release");
  }
}

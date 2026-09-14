import { createServer, type Server } from "node:http";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { createHash } from "node:crypto";

// A disposable read-only OCI registry backed by the real package builder's layout.
export class Registry {
  readonly host: string;
  readonly reference: string;
  private constructor(private readonly server: Server, port: number) {
    this.host = `127.0.0.1:${port}`;
    this.reference = `${this.host}/your-org/cloud_dev:latest`;
  }

  static async create(layout: string): Promise<Registry> {
    const index = JSON.parse(await readFile(join(layout, "index.json"), "utf8"));
    const manifestDigest: string = index.manifests[0].digest;
    const server = createServer((request, response) => {
      void (async () => {
        const path = request.url ?? "";
        let digest = path.slice(path.lastIndexOf("/") + 1);
        const isManifest = path.startsWith("/v2/your-org/cloud_dev/manifests/");
        if (isManifest && ["latest", "alias"].includes(digest)) digest = manifestDigest;
        if ((!isManifest && !path.startsWith("/v2/your-org/cloud_dev/blobs/")) || !/^sha256:[a-f0-9]{64}$/.test(digest)) {
          response.writeHead(404).end(); return;
        }
        const content = await readFile(join(layout, "blobs/sha256", digest.slice(7)));
        response.setHeader("Content-Type", isManifest ? "application/vnd.oci.image.manifest.v1+json" : "application/octet-stream");
        response.setHeader("Content-Length", content.length);
        response.setHeader("Docker-Content-Digest", `sha256:${createHash("sha256").update(content).digest("hex")}`);
        response.end(request.method === "HEAD" ? undefined : content);
      })().catch(() => response.writeHead(404).end());
    });
    await new Promise<void>((ok, fail) => { server.once("error", fail); server.listen(0, "127.0.0.1", ok); });
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("No registry port assigned");
    return new Registry(server, address.port);
  }

  async close(): Promise<void> {
    await new Promise<void>((ok, fail) => this.server.close(error => error ? fail(error) : ok()));
  }
}

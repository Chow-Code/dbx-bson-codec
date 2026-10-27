import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdir, mkdtemp } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import readline from "node:readline";

const root = fileURLToPath(new URL("..", import.meta.url));
await mkdir(path.join(root, "work"), { recursive: true });
const dataDir = await mkdtemp(path.join(root, "work", "smoke-"));
const executable = process.argv[2] || path.join(root, "bin", "windows-x64", "backend.exe");
const child = spawn(executable, [], { windowsHide: true, stdio: ["pipe", "pipe", "pipe"], env: { ...process.env, DBX_PLUGIN_DATA_DIR: dataDir } });
const requests = new Map();
let requestId = 0;
let stderr = "";
child.stderr.on("data", (data) => { stderr = (stderr + data.toString()).slice(-16384); });
child.on("error", (error) => { for (const pending of requests.values()) pending.reject(error); });
child.on("exit", (code) => { for (const pending of requests.values()) pending.reject(new Error(`Backend exited (${code}): ${stderr}`)); });
readline.createInterface({ input: child.stdout }).on("line", (line) => {
  const response = JSON.parse(line);
  const pending = requests.get(response.id);
  if (!pending) return;
  requests.delete(response.id);
  if (response.error) pending.reject(new Error(response.error.message));
  else pending.resolve(response.result);
});
function call(method, params) {
  const id = ++requestId;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => { requests.delete(id); reject(new Error(`RPC timeout: ${method}`)); }, 12000);
    requests.set(id, { resolve(value) { clearTimeout(timer); resolve(value); }, reject(error) { clearTimeout(timer); reject(error); } });
    child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
  });
}
try {
  const initialized = await call("plugin/initialize", { host: { dbxVersion: "0.6.51", hostApiVersion: "1.0.0", protocolVersions: [1] }, plugin: { id: "io.github.chow-code.redis-bson-codec", version: "0.1.0" }, permissions: [] });
  assert.equal(initialized.protocolVersion, 1);
  // Independent wire fixture: BSON specification example {hello: "world"}.
  const bsonRaw = Buffer.from("160000000268656c6c6f0006000000776f726c640000", "hex");
  const bsonContext = { db: 0, keyRaw: "", redisType: "string" };
  const bsonDecoded = await call("redis/codec/decode", { codecId: "bson", rawBase64: bsonRaw.toString("base64"), context: bsonContext });
  assert.deepEqual(bsonDecoded, { format: "json", value: { hello: "world" } });
  const bsonEncoded = await call("redis/codec/encode", { codecId: "bson", ...bsonDecoded, context: bsonContext });
  assert.equal(bsonEncoded.rawBase64, bsonRaw.toString("base64"));
  const bsonTyped = { playerId: { $numberLong: "9223372036854775807" }, _id: { $oid: "0102030405060708090a0b0c" }, bytes: { $binary: { base64: "AP+A", subType: "00" } }, price: { $numberDecimal: "123.4500" }, created: { $date: { $numberLong: "-1000" } } };
  const typedEncoded = await call("redis/codec/encode", { codecId: "bson", format: "json", value: bsonTyped, context: bsonContext });
  const typedDecoded = await call("redis/codec/decode", { codecId: "bson", ...typedEncoded, context: bsonContext });
  assert.deepEqual(typedDecoded.value, bsonTyped);
  await assert.rejects(call("redis/codec/decode", { codecId: "bson", rawBase64: "AA==", context: bsonContext }), /BSON/);
  console.log("PASS: configuration-free bundled BSON subprocess, canonical types including exact Int64, byte-exact known fixture round trip, malformed BSON rejection");
  const script = path.join(root, "examples", "xor-json.mjs");
  const config = { decode: { program: process.execPath, args: [script, "decode"] }, encode: { program: process.execPath, args: [script, "encode"] }, format: "json", timeoutMs: 3000 };
  await call("config/save", config);
  assert.deepEqual(await call("config/get", {}), config);
  const value = { playerId: "9007199254740993", name: "示例玩家", level: 42, equipment: ["弓", "盾"] };
  const raw = Buffer.from(JSON.stringify(value)).map((byte) => byte ^ 0xa5);
  const context = { db: 2, keyRaw: "cGxheWVyOjE=", redisType: "hash" };
  const decoded = await call("redis/codec/decode", { codecId: "external-program", rawBase64: raw.toString("base64"), context });
  assert.equal(decoded.format, "json"); assert.deepEqual(decoded.value, value);
  const encoded = await call("redis/codec/encode", { codecId: "external-program", ...decoded, context });
  assert.equal(encoded.rawBase64, raw.toString("base64"));
  await assert.rejects(call("redis/codec/decode", { codecId: "external-program", rawBase64: "!", context }), /base64/);
  await call("config/save", { ...config, decode: { program: process.execPath, args: ["-e", 'process.stdout.write("invalid JSON")'] } });
  await assert.rejects(call("redis/codec/decode", { codecId: "external-program", rawBase64: "AA==", context }), /valid JSON/);
  await call("config/save", config);
  console.log("PASS: real packaged backend handshake, config persistence, external Node decoder, byte-exact encode round trip, and failure propagation");
} finally {
  const exited = once(child, "exit"); child.stdin.end();
  const timer = setTimeout(() => child.kill(), 3000);
  await exited; clearTimeout(timer);
}

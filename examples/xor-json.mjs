// Demonstration only: reversible XOR is not encryption. No npm dependencies.
// Configure: program=<absolute node.exe>, args=[<absolute this file>, "decode"|"encode"].
const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const input = Buffer.concat(chunks);
const operation = process.argv[2];
if (operation !== "decode" && operation !== "encode") throw new Error("Expected decode or encode");
const result = Buffer.alloc(input.length);
for (let index = 0; index < input.length; index++) result[index] = input[index] ^ 0xa5;
if (operation === "decode") JSON.parse(result.toString("utf8"));
else JSON.parse(input.toString("utf8"));
process.stdout.write(result);

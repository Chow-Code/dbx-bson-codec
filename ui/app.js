/* DBX injects dbxPlugin; no Node, Tauri, connection or credential access. */
const $ = (id) => document.getElementById(id);
const bridge = window.dbxPlugin;
const zh = (bridge?.locale ?? navigator.language).startsWith("zh");
const messages = zh ? {
  title: "Redis BSON 编解码", intro: "内置 BSON 解码器无需配置。Redis 值详情中选择“插件”及 BSON，即可查看保留类型和大整数精度的 Extended JSON。自定义程序只在选择对应解码器时执行。",
  configHeading: "外部程序", configHelp: "program 填可执行程序的绝对路径，args 是参数数组。解码器从标准输入读取原始二进制，并向标准输出写 JSON 或 UTF-8 文本；编码器执行相反操作。配置保存在本机。",
  save: "保存配置", reload: "重新加载", testHeading: "测试转换", run: "运行", roundtrip: "验证往返转换", saved: "已保存。", same: "往返转换成功：字节完全一致。", different: "往返转换未保持原始字节。", input: "请填写输入内容。", unavailable: "请在 DBX 插件工作台中打开此页面。"
} : {
  title: "Redis BSON Codec", intro: "BSON works without configuration. In Redis value details choose Plugin, then BSON, to view type-preserving Extended JSON. Custom programs run only when their codec is selected.",
  configHeading: "External programs", configHelp: "program is an absolute executable path; args is an argument array. The decoder reads raw bytes from stdin and writes JSON or UTF-8 text to stdout. The encoder does the reverse. Configuration stays on this machine.",
  save: "Save configuration", reload: "Reload", testHeading: "Test conversion", run: "Run", roundtrip: "Verify round trip", saved: "Saved.", same: "Round trip passed: bytes are identical.", different: "Round trip did not preserve the original bytes.", input: "Enter test input.", unavailable: "Open this page in a DBX plugin workbench."
};
for (const id of ["title", "intro", "configHeading", "configHelp", "save", "reload", "testHeading", "run", "roundtrip"]) $(id).textContent = messages[id];
$("sample").textContent = zh ? "载入 BSON 示例" : "Load BSON sample";
$("codecLabel").textContent = zh ? "编解码器" : "Codec";
const updateConfigVisibility = () => { $("config").closest("section").hidden = $("codec").value !== "external-program"; $("result").textContent = ""; $("error").textContent = ""; $("status").textContent = ""; };
$("codec").onchange = updateConfigVisibility;
updateConfigVisibility();
$("sample").onclick = () => { $("codec").value = "bson"; updateConfigVisibility(); $("operation").value = "decode"; $("testInput").value = "FgAAAAJoZWxsbwAGAAAAd29ybGQAAA=="; };
let busy = false;
async function action(work) {
  if (busy) return;
  busy = true; $("error").textContent = ""; $("status").textContent = "";
  for (const control of document.querySelectorAll("button,select,textarea")) control.disabled = true;
  try { if (!bridge) throw new Error(messages.unavailable); await bridge.ready; await work(); }
  catch (error) { $("error").textContent = String(error); }
  finally { busy = false; for (const control of document.querySelectorAll("button,select,textarea")) control.disabled = false; }
}
const context = { db: 0, keyRaw: "", redisType: "string" };
const invoke = (operation, values) => bridge.invoke(`redis/codec/${operation}`, { codecId: $("codec").value, context, ...values }, { timeoutMs: 10000 });
async function load() { $("config").value = JSON.stringify(await bridge.invoke("config/get", {}), null, 2); }
$("reload").onclick = () => action(load);
$("save").onclick = () => action(async () => { await bridge.invoke("config/save", JSON.parse($("config").value)); $("status").textContent = messages.saved; });
$("run").onclick = () => action(async () => {
  $("result").textContent = "";
  const input = $("testInput").value;
  if (!input) throw new Error(messages.input);
  const format = $("inputFormat").value;
  const result = await invoke($("operation").value, $("operation").value === "decode" ? { rawBase64: input.trim() } : { format, value: format === "json" ? JSON.parse(input) : input });
  $("result").textContent = JSON.stringify(result, null, 2);
});
$("roundtrip").onclick = () => action(async () => {
  $("result").textContent = "";
  const rawBase64 = $("testInput").value.trim();
  if (!rawBase64) throw new Error(messages.input);
  const decoded = await invoke("decode", { rawBase64 });
  const encoded = await invoke("encode", { format: decoded.format, value: decoded.value });
  $("result").textContent = JSON.stringify(encoded, null, 2);
  $("status").textContent = encoded.rawBase64 === rawBase64 ? messages.same : messages.different;
});
void action(load);

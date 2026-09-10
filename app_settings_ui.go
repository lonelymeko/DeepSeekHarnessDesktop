package main

import "fmt"

// injectDesktopSettings adds the desktop shell's own settings surface: a gear
// that opens a modal holding the outbound-network preference and the DeepSeek
// account's balance and usage.
//
// It is deliberately self-contained — a fixed overlay, no dependency on the
// Harness document's own structure — because the upstream web app is replaced
// wholesale on every release, and a section injected into its settings page
// would break the first time that page is refactored.
func injectDesktopSettings(document []byte, platform string) []byte {
	// The gear lives in the desktop title bar when the shell injects one, and
	// floats clear of the page otherwise.
	anchoredRight, anchoredHeight := "8px", "44px"
	switch platform {
	case "windows":
		anchoredRight, anchoredHeight = "116px", "32px"
	case "darwin":
		// macOS keeps the traffic lights at the far left and needs no controls
		// on the right, so the gear sits in the corner the drag region leaves.
	default:
		anchoredRight, anchoredHeight = "", ""
	}

	anchorRule := ""
	if anchoredRight != "" {
		anchorRule = fmt.Sprintf(
			"#dsh-desktop-titlebar #dsh-desktop-settings-toggle{position:absolute;top:0;right:%s;width:36px;height:%s;border-radius:0}",
			anchoredRight, anchoredHeight)
	}

	content := []byte(`<style id="dsh-desktop-settings-style">
#dsh-desktop-settings-toggle{position:fixed;top:12px;right:12px;z-index:2147483644;width:34px;height:34px;display:flex;align-items:center;justify-content:center;padding:0;border:1px solid rgba(0,0,0,.12);border-radius:50%;background:rgba(255,255,255,.86);color:#333;font:15px/1 system-ui,-apple-system,"Segoe UI",sans-serif;cursor:pointer;backdrop-filter:blur(12px);-webkit-backdrop-filter:blur(12px);--wails-draggable:no-drag}
#dsh-desktop-settings-toggle:hover{background:rgba(240,240,240,.95)}
body[data-ds-dark-theme] #dsh-desktop-settings-toggle{background:rgba(36,36,36,.86);color:#eee;border-color:rgba(255,255,255,.14)}
body[data-ds-dark-theme] #dsh-desktop-settings-toggle:hover{background:rgba(58,58,58,.92)}
` + anchorRule + `
#dsh-desktop-settings-backdrop{position:fixed;inset:0;z-index:2147483646;background:rgba(0,0,0,.42)}
#dsh-desktop-settings-backdrop[hidden]{display:none}
#dsh-desktop-settings{position:fixed;top:50%;left:50%;transform:translate(-50%,-50%);z-index:2147483647;width:min(560px,calc(100vw - 40px));max-height:min(760px,calc(100vh - 60px));overflow:auto;box-sizing:border-box;padding:20px;border-radius:12px;border:1px solid rgba(0,0,0,.12);background:#fff;color:#191919;box-shadow:0 24px 64px rgba(0,0,0,.34);font:14px/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;letter-spacing:0}
body[data-ds-dark-theme] #dsh-desktop-settings{background:#242424;color:#f5f5f5;border-color:rgba(255,255,255,.14)}
#dsh-desktop-settings[hidden],#dsh-desktop-settings-backdrop[hidden]{display:none}
#dsh-desktop-settings h2{margin:0;font-size:17px;font-weight:650}
#dsh-desktop-settings h3{margin:0 0 8px;font-size:13px;font-weight:650;color:#555;text-transform:none}
body[data-ds-dark-theme] #dsh-desktop-settings h3{color:#b9b9b9}
.dsh-desktop-settings-head{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:14px}
.dsh-desktop-settings-group{padding:14px;margin-bottom:14px;border:1px solid rgba(0,0,0,.1);border-radius:8px}
body[data-ds-dark-theme] .dsh-desktop-settings-group{border-color:rgba(255,255,255,.12)}
.dsh-desktop-settings-group:last-of-type{margin-bottom:0}
.dsh-desktop-settings-check{display:flex;align-items:center;gap:8px;font-weight:600;cursor:pointer}
.dsh-desktop-settings-check input{width:16px;height:16px;margin:0;cursor:pointer}
.dsh-desktop-settings-hint{margin:8px 0 0;color:#666;font-size:12.5px;overflow-wrap:anywhere}
body[data-ds-dark-theme] .dsh-desktop-settings-hint{color:#b0b0b0}
.dsh-desktop-settings-row{display:flex;align-items:baseline;justify-content:space-between;gap:12px;padding:3px 0}
.dsh-desktop-settings-row span:first-child{color:#666}
body[data-ds-dark-theme] .dsh-desktop-settings-row span:first-child{color:#b0b0b0}
.dsh-desktop-settings-row span:last-child{font-variant-numeric:tabular-nums;font-weight:600}
.dsh-desktop-settings-balance{padding:8px 0 2px}
.dsh-desktop-settings-status{display:inline-flex;align-items:center;gap:6px;font-weight:600}
.dsh-desktop-settings-dot{width:8px;height:8px;border-radius:50%;background:#9aa0a6}
.dsh-desktop-settings-dot[data-state="ok"]{background:#16865b}
.dsh-desktop-settings-dot[data-state="warn"]{background:#c77700}
.dsh-desktop-settings-dot[data-state="bad"]{background:#c42b1c}
.dsh-desktop-settings-models{margin:6px 0 0;padding:0;list-style:none;font-size:12.5px}
.dsh-desktop-settings-models li{display:flex;justify-content:space-between;gap:12px;padding:2px 0;border-top:1px solid rgba(127,127,127,.16)}
.dsh-desktop-settings-models li:first-child{border-top:0}
.dsh-desktop-settings-models code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
.dsh-desktop-settings-error{margin:8px 0 0;color:#c42b1c;font-size:12.5px;overflow-wrap:anywhere}
body[data-ds-dark-theme] .dsh-desktop-settings-error{color:#ff9d92}
.dsh-desktop-settings-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:12px}
.dsh-desktop-settings-actions button,#dsh-desktop-settings-close{min-height:32px;padding:5px 12px;border:1px solid rgba(0,0,0,.16);border-radius:6px;background:transparent;color:inherit;font:600 13px/1.2 system-ui,-apple-system,"Segoe UI",sans-serif;cursor:pointer}
body[data-ds-dark-theme] .dsh-desktop-settings-actions button,body[data-ds-dark-theme] #dsh-desktop-settings-close{border-color:rgba(255,255,255,.2)}
.dsh-desktop-settings-actions button:hover,#dsh-desktop-settings-close:hover{background:rgba(127,127,127,.12)}
.dsh-desktop-settings-actions button:disabled{opacity:.58;cursor:default}
</style>
<button id="dsh-desktop-settings-toggle" type="button" title="桌面设置" aria-label="桌面设置" aria-expanded="false">&#9881;</button>
<div id="dsh-desktop-settings-backdrop" hidden></div>
<aside id="dsh-desktop-settings" role="dialog" aria-modal="true" aria-label="桌面设置" hidden>
<div class="dsh-desktop-settings-head"><h2>桌面设置</h2><button id="dsh-desktop-settings-close" type="button" aria-label="关闭">关闭</button></div>
<section class="dsh-desktop-settings-group">
<h3>网络</h3>
<label class="dsh-desktop-settings-check"><input id="dsh-desktop-settings-proxy" type="checkbox">网络请求走系统代理</label>
<p class="dsh-desktop-settings-hint" id="dsh-desktop-settings-proxy-detail">正在读取…</p>
</section>
<section class="dsh-desktop-settings-group">
<h3>DeepSeek 官方用量与额度</h3>
<div class="dsh-desktop-settings-balance" id="dsh-desktop-settings-balance"></div>
<div id="dsh-desktop-settings-usage"></div>
<p class="dsh-desktop-settings-hint" id="dsh-desktop-settings-account"></p>
<div class="dsh-desktop-settings-actions"><button id="dsh-desktop-settings-refresh" type="button">刷新</button></div>
</section>
</aside>
<script id="dsh-desktop-settings">(() => {
const toggle = document.getElementById("dsh-desktop-settings-toggle");
const panel = document.getElementById("dsh-desktop-settings");
const backdrop = document.getElementById("dsh-desktop-settings-backdrop");
const close = document.getElementById("dsh-desktop-settings-close");
const proxyBox = document.getElementById("dsh-desktop-settings-proxy");
const proxyDetail = document.getElementById("dsh-desktop-settings-proxy-detail");
const balanceBox = document.getElementById("dsh-desktop-settings-balance");
const usageBox = document.getElementById("dsh-desktop-settings-usage");
const account = document.getElementById("dsh-desktop-settings-account");
const refresh = document.getElementById("dsh-desktop-settings-refresh");
if (!toggle || !panel) return;

// The gear sits in the desktop title bar when the shell injects one.
const titlebar = document.getElementById("dsh-desktop-titlebar");
if (titlebar) titlebar.appendChild(toggle);

const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function desktopAPI() {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    const candidate = window.go && window.go.main && window.go.main.App;
    if (candidate && candidate.GetDesktopNetworkState) return candidate;
    await wait(250);
  }
  return null;
}
function message(error) {
  if (error && typeof error.message === "string" && error.message !== "") return error.message;
  return String(error);
}
function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }
function row(label, value) {
  const line = document.createElement("div");
  line.className = "dsh-desktop-settings-row";
  const left = document.createElement("span");
  left.textContent = label;
  const right = document.createElement("span");
  right.textContent = value;
  line.appendChild(left);
  line.appendChild(right);
  return line;
}
function hint(text) {
  const line = document.createElement("p");
  line.className = "dsh-desktop-settings-hint";
  line.textContent = text;
  return line;
}
function failure(text) {
  const line = document.createElement("p");
  line.className = "dsh-desktop-settings-error";
  line.textContent = text;
  return line;
}
function group(title) {
  const section = document.createElement("div");
  const heading = document.createElement("h3");
  heading.textContent = title;
  section.appendChild(heading);
  return section;
}
function amount(value) {
  const number = Number(value);
  return Number.isFinite(number) ? number.toLocaleString("zh-CN") : String(value);
}
function money(value, currency) {
  const number = Number(value) || 0;
  const symbol = String(currency || "").toUpperCase() === "USD" ? "$" : "\u00a5";
  return symbol + number.toFixed(2);
}

function setOpen(open) {
  panel.hidden = !open;
  backdrop.hidden = !open;
  toggle.setAttribute("aria-expanded", open ? "true" : "false");
  if (open) void refreshAll();
}
function renderNetwork(state) {
  if (!state) return;
  proxyBox.checked = state.useSystemProxy === true;
  const parts = [];
  if (state.routed) {
    const address = state.http || state.https || state.socks || "";
    parts.push("当前走代理 " + address + (state.source ? "（" + state.source + "）" : ""));
  } else {
    parts.push("当前直连：系统未配置代理，环境变量也没有 http_proxy。");
  }
  if (state.noProxy) parts.push("直连名单 " + state.noProxy);
  if (state.restartRequired) parts.push("已保存。重新打开应用后 Harness 自身的模型请求才会完全跟随该设置。");
  proxyDetail.textContent = parts.join(" ");
}
async function loadNetwork() {
  const api = await desktopAPI();
  if (!api) { proxyDetail.textContent = "桌面接口尚未就绪。"; return; }
  try {
    renderNetwork(await api.GetDesktopNetworkState());
  } catch (error) {
    proxyDetail.textContent = "读取网络设置失败：" + message(error);
  }
}
function renderDeepSeek(overview) {
  clear(balanceBox);
  clear(usageBox);
  if (!overview) { account.textContent = ""; return; }
  if (!overview.apiKeyConfigured) {
    account.textContent = "未检测到 DEEPSEEK_API_KEY。在 Harness 的模型设置里保存 DeepSeek API Key 后即可显示余额。";
  } else {
    account.textContent = "API Key 来源：" + (overview.apiKeySource || "未知");
  }

  const balanceSection = group("额度（GET /user/balance）");
  if (overview.balance) {
    const status = document.createElement("p");
    status.className = "dsh-desktop-settings-hint";
    const dot = document.createElement("span");
    dot.className = "dsh-desktop-settings-dot";
    dot.setAttribute("data-state", overview.balance.available ? "ok" : "bad");
    const label = document.createElement("span");
    label.textContent = overview.balance.available ? "账户可用" : "余额不足，请充值";
    const wrap = document.createElement("span");
    wrap.className = "dsh-desktop-settings-status";
    wrap.appendChild(dot);
    wrap.appendChild(label);
    status.appendChild(wrap);
    balanceSection.appendChild(status);
    const infos = Array.isArray(overview.balance.infos) ? overview.balance.infos : [];
    if (infos.length === 0) balanceSection.appendChild(hint("接口没有返回余额明细。"));
    infos.forEach((info) => {
      const currency = info.currency || "CNY";
      balanceSection.appendChild(row(currency + " 总余额", info.totalBalance || "0"));
      balanceSection.appendChild(row(currency + " 充值余额", info.toppedUpBalance || "0"));
      balanceSection.appendChild(row(currency + " 赠送余额", info.grantedBalance || "0"));
    });
  } else if (overview.balanceError) {
    balanceSection.appendChild(failure("读取失败：" + overview.balanceError));
  } else if (overview.apiKeyConfigured) {
    balanceSection.appendChild(hint("尚未读取。"));
  } else {
    balanceSection.appendChild(hint("需要 API Key。"));
  }
  balanceBox.appendChild(balanceSection);

  const usageSection = group("本月用量与消费");
  if (overview.usage) {
    const usage = overview.usage;
    const currency = usage.currency || "CNY";
    usageSection.appendChild(row("Token 合计", amount(usage.month && usage.month.tokens)));
    usageSection.appendChild(row("请求次数", amount(usage.month && usage.month.requests)));
    usageSection.appendChild(row("消费", money(usage.month && usage.month.cost, currency)));
    usageSection.appendChild(row("今日 Token", amount(usage.today && usage.today.tokens)));
    usageSection.appendChild(row("今日消费", money(usage.today && usage.today.cost, currency)));
    const breakdown = usage.breakdown || {};
    usageSection.appendChild(row("缓存命中 Token", amount(breakdown.cacheHit)));
    usageSection.appendChild(row("缓存未命中 Token", amount(breakdown.cacheMiss)));
    usageSection.appendChild(row("输出 Token", amount(breakdown.output)));
    const models = Array.isArray(usage.models) ? usage.models : [];
    if (models.length > 0) {
      const list = document.createElement("ul");
      list.className = "dsh-desktop-settings-models";
      models.slice(0, 12).forEach((model) => {
        const item = document.createElement("li");
        const name = document.createElement("code");
        name.textContent = model.model || "(unknown)";
        const value = document.createElement("span");
        value.textContent = amount(model.tokens) + " · " + money(model.cost, currency);
        item.appendChild(name);
        item.appendChild(value);
        list.appendChild(item);
      });
      usageSection.appendChild(list);
    }
  } else if (overview.usageError) {
    usageSection.appendChild(failure("读取失败：" + overview.usageError));
  } else if (overview.userTokenConfigured) {
    usageSection.appendChild(hint("尚未读取。"));
  } else {
    usageSection.appendChild(hint("用量接口是 platform.deepseek.com 的登录态接口，需要在 Harness 凭据里保存 DEEPSEEK_USER_TOKEN（浏览器控制台执行 localStorage.getItem(\"userToken\") 取得）。"));
  }
  usageBox.appendChild(usageSection);

  if (overview.proxyRouted) {
    account.textContent += "　｜　已走代理" + (overview.proxySource ? "（" + overview.proxySource + "）" : "");
  }
}
async function loadDeepSeek() {
  const api = await desktopAPI();
  if (!api || !api.GetDeepSeekOverview) return;
  account.textContent = "正在读取 DeepSeek 用量…";
  try {
    renderDeepSeek(await api.GetDeepSeekOverview());
  } catch (error) {
    clear(balanceBox);
    clear(usageBox);
    account.textContent = "读取失败：" + message(error);
  }
}
async function refreshAll() {
  refresh.disabled = true;
  try {
    await loadNetwork();
    await loadDeepSeek();
  } finally {
    refresh.disabled = false;
  }
}
toggle.addEventListener("click", () => setOpen(panel.hidden));
close.addEventListener("click", () => setOpen(false));
backdrop.addEventListener("click", () => setOpen(false));
document.addEventListener("keydown", (event) => { if (event.key === "Escape" && !panel.hidden) setOpen(false); });
refresh.addEventListener("click", () => void refreshAll());
proxyBox.addEventListener("change", async () => {
  const api = await desktopAPI();
  if (!api || !api.SetNetworkUseSystemProxy) return;
  proxyBox.disabled = true;
  try {
    renderNetwork(await api.SetNetworkUseSystemProxy(proxyBox.checked));
  } catch (error) {
    proxyDetail.textContent = "保存失败：" + message(error);
  } finally {
    proxyBox.disabled = false;
  }
});
// The proxy setting is a launch-time fact worth showing without opening the panel.
loadNetwork();
})()</script>`)
	return injectBeforeClosingBody(document, content)
}

const $ = (selector) => document.querySelector(selector);
const ui = { connected: false, autoMode: false, selectedNode: null, nodes: [], routing: null, trafficMode: "all", displayMode: "all", deviceIPs: [], deviceMACs: [], devicePolicies: [], devices: [] };
let trafficDirty = false;

if (new URLSearchParams(location.search).has("demo")) {
  const banner = document.createElement("div");
  banner.className = "demo-banner";
  banner.textContent = "ДЕМО · соединение имитируется, VPN-трафик не передаётся";
  document.querySelector(".topbar").after(banner);
}

async function api(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { ...(options.body ? { "Content-Type": "application/json" } : {}), ...options.headers } });
  if (!response.ok) {
    let message = `Ошибка ${response.status}`;
    try { message = (await response.json()).error || message; } catch {}
    throw new Error(message);
  }
  return response.status === 204 ? null : response.json();
}

function notice(message, error = false) {
  const element = $("#notice");
  element.textContent = message;
  element.classList.toggle("error", error);
  element.hidden = false;
  clearTimeout(notice.timer);
  if (!error) notice.timer = setTimeout(() => { element.hidden = true; }, 6500);
}

function updateConnection() {
  const pill = $("#connection-pill");
  pill.classList.toggle("connected", ui.connected);
  pill.classList.toggle("disconnected", !ui.connected);
  $("#connection-label").textContent = ui.connected ? (ui.autoMode ? "АВТО · ONLINE" : "ONLINE") : (ui.autoMode ? "АВТО · ПОИСК" : "OFFLINE");
  $("#hero-title").textContent = ui.connected ? "VPN подключён" : (ui.autoMode ? "Авто восстанавливает VPN" : (ui.transportActive ? "Проверяем соединение" : "Ваш VPN готов"));
  const modeLabel = ({ all: "для всего трафика", direct: "напрямую по умолчанию", geoblock: "по правилам геоблока", telegram: "только для Telegram", youtube: "только для YouTube", happ: "по правилам Happ" })[ui.displayMode] || "по выбранным правилам";
  const count = ui.devicePolicies.length;
  const deviceLabel = count ? ` · отдельных правил: ${count}` : "";
  $("#hero-description").textContent = ui.autoMode
    ? `${ui.selectedNode?.name || "Поиск рабочего сервера"} · авто проверяет VPN и переключит сервер при сбое.`
    : ui.connected
      ? `${ui.selectedNode?.name || "Сервер выбран"} · VPN ${modeLabel}${deviceLabel}`
      : (ui.selectedNode ? `Выбран сервер: ${ui.selectedNode.name}` : "Добавьте ссылку на подписку и выберите сервер.");
  $("#connect").textContent = ui.connected ? "Применить изменения" : "Подключиться";
  $("#connect").disabled = !ui.selectedNode;
  $("#disconnect").disabled = !ui.vpnEnabled && !ui.transportActive && !ui.autoMode;
  $("#auto-connect").disabled = ui.nodes.length === 0;
  $("#auto-connect").textContent = ui.autoMode ? "Отключить авто" : "Авто · лучший сервер";
}

async function loadStatus() {
  const status = await api("/api/status");
  ui.connected = status.connected;
 ui.transportActive = Boolean(status.transportActive);
 ui.vpnEnabled = Boolean(status.vpnEnabled);
  ui.autoMode = Boolean(status.autoMode);
  ui.selectedNode = status.selectedNode;
  ui.trafficMode = status.trafficMode || (status.routing ? "happ" : "all");
  ui.deviceIPs = status.deviceIPs || [];
  ui.deviceMACs = status.deviceMACs || [];
  const legacy = !status.devicePolicies?.length && (ui.deviceIPs.length || ui.deviceMACs.length);
  ui.devicePolicies = legacy
    ? [...ui.deviceMACs.map(mac => ({ mac, mode: ui.trafficMode })), ...ui.deviceIPs.map(ip => ({ ip, mode: ui.trafficMode }))]
    : (status.devicePolicies || []);
  ui.displayMode = legacy ? "direct" : ui.trafficMode;
  const routingData = await api("/api/routing");
  ui.routing = routingData.profile;
  renderRouting();
  renderTraffic();
  updateConnection();
}

function renderTraffic() {
  $("#traffic-mode").querySelector('[value="happ"]').disabled = !ui.routing;
  if (trafficDirty) return;
  $("#traffic-mode").value = ui.displayMode;
  renderDevices();
}

const policyModes = [
  ["", "Как для всей сети"], ["all", "Всё через VPN"], ["direct", "Напрямую"],
  ["geoblock", "Геоблок"], ["telegram", "Telegram"], ["youtube", "YouTube"], ["happ", "Свой Happ"],
];

function createDeviceRow(policy, caption, removable = false) {
  const row = document.createElement("div"); row.className = "device-option";
  if (policy.mac) row.dataset.mac = policy.mac;
  if (policy.ip) row.dataset.ip = policy.ip;
  const title = document.createElement("span"); title.className = "device-caption"; title.textContent = caption;
  const select = document.createElement("select"); select.className = "device-policy-mode";
  select.setAttribute("aria-label", `Режим для ${caption}`);
  for (const [value, label] of policyModes) {
    const option = document.createElement("option"); option.value = value; option.textContent = label;
    option.disabled = value === "happ" && !ui.routing && policy.mode !== "happ";
    select.append(option);
  }
  select.value = policy.mode || "";
  select.addEventListener("change", () => { trafficDirty = true; });
  row.append(title, select);
  if (removable) {
    const remove = document.createElement("button"); remove.type = "button"; remove.className = "remove-profile";
    remove.textContent = "Убрать"; remove.setAttribute("aria-label", `Убрать ${caption}`);
    remove.addEventListener("click", () => { row.remove(); trafficDirty = true; });
    row.append(remove);
  }
  return row;
}

function renderDevices() {
  if (trafficDirty) return;
  const container = $("#device-picker");
  container.replaceChildren();
  const discovered = new Set();
  for (const device of ui.devices) {
    discovered.add(device.mac);
    const policy = ui.devicePolicies.find(item => item.mac === device.mac);
    container.append(createDeviceRow({ mac: device.mac, mode: policy?.mode || "" }, `${device.name || device.mac} · ${device.ips.join(", ")} · ${device.mac}`));
  }
  for (const policy of ui.devicePolicies) {
    if (policy.mac && discovered.has(policy.mac)) continue;
    container.append(createDeviceRow(policy, `${policy.mac || policy.ip} · вручную`, true));
  }
  if (!container.children.length) {
    const empty = document.createElement("span"); empty.className = "muted";
    empty.textContent = "Устройства пока не обнаружены. Добавьте MAC или IP вручную.";
    container.append(empty);
  }
}

async function loadDevices() {
  ui.devices = await api("/api/devices");
  renderDevices();
  const traffic = await api("/api/device-traffic");
  const container = $("#device-traffic"); container.replaceChildren();
  if (!traffic.available) {
    const message = document.createElement("p"); message.className = "muted";
    message.textContent = `Счётчики недоступны: ${traffic.reason}`;
    container.append(message); return;
  }
  if (!traffic.devices.length) {
    const empty = document.createElement("p"); empty.className = "empty";
    empty.textContent = "Устройства пока не обнаружены."; container.append(empty); return;
  }
  for (const device of traffic.devices) {
    const row = document.createElement("div"); row.className = "traffic-row";
    const name = document.createElement("span"); name.textContent = `${device.name || device.mac} · ${device.ips.join(", ")}`;
    const bytes = document.createElement("strong");
    bytes.textContent = `↑ ${formatBytes(device.uploadBytes)}  ↓ ${formatBytes(device.downloadBytes)}`;
    row.append(name, bytes); container.append(row);
  }
}

function formatBytes(value) {
  if (value < 1024) return `${value} Б`;
  const units = ["КБ", "МБ", "ГБ", "ТБ"];
  let scaled = value;
  let index = -1;
  do { scaled /= 1024; index++; } while (scaled >= 1024 && index < units.length - 1);
  return `${scaled.toFixed(1)} ${units[index]}`;
}

async function loadSubscriptions() {
  const items = await api("/api/subscriptions");
  const container = $("#subscriptions");
  container.replaceChildren();
  for (const sub of items) {
    const row = document.createElement("div"); row.className = "subscription-item";
    const details = document.createElement("div");
    const title = document.createElement("div"); title.className = "subscription-title"; title.textContent = sub.name;
    const meta = document.createElement("div"); meta.className = "subscription-meta";
    const updatedAt = sub.updatedAt ? new Date(sub.updatedAt).toLocaleString("ru-RU", { dateStyle: "short", timeStyle: "short" }) : "ещё не проверялась";
    const interval = Number(sub.updateIntervalHours) || 24;
    meta.textContent = `${sub.nodeCount} серверов · проверено ${updatedAt} · автообновление раз в ${interval} ч.`;
    details.append(title, meta);
    const update = document.createElement("button"); update.type = "button"; update.className = "button secondary subscription-update"; update.textContent = "Обновить";
    update.title = "Скачать текущую версию подписки и заменить список серверов";
    update.addEventListener("click", async () => {
      update.disabled = true; update.textContent = "Обновляю…";
      try {
        const result = await api(`/api/subscriptions/${encodeURIComponent(sub.id)}/update`, { method: "POST" });
        await refresh();
        notice(`Подписка обновлена · ${result.nodeCount} серверов.`);
      } catch (error) {
        notice(error.message, true);
        await refresh().catch(() => {});
      } finally {
        update.disabled = false; update.textContent = "Обновить";
      }
    });
    const remove = document.createElement("button"); remove.type = "button"; remove.className = "button secondary subscription-update"; remove.textContent = "Удалить";
    remove.addEventListener("click", async () => {
      if (!window.confirm(`Удалить подписку «${sub.name}»?`)) return;
      remove.disabled = true;
      try { await api(`/api/subscriptions/${encodeURIComponent(sub.id)}`, { method: "DELETE" }); await refresh(); notice("Подписка удалена."); }
      catch (error) { remove.disabled = false; notice(error.message, true); }
    });
    const actions = document.createElement("div"); actions.className = "subscription-actions"; actions.append(update, remove);
    row.append(details, actions); container.append(row);
  }
}

async function loadNodes() {
  ui.nodes = await api("/api/nodes");
  $("#node-count").textContent = ui.nodes.length;
  $("#auto-connect").disabled = ui.nodes.length === 0;
  $("#auto-connect").textContent = ui.autoMode ? "Отключить авто" : "Авто · лучший сервер";
  const container = $("#nodes"); container.replaceChildren();
  if (!ui.nodes.length) {
    const empty = document.createElement("p"); empty.className = "empty"; empty.textContent = "Добавьте подписку, чтобы увидеть серверы."; container.append(empty); return;
  }
  for (const node of ui.nodes) {
    const row = document.createElement("button"); row.type = "button"; row.className = `node${node.selected ? " selected" : ""}`;
    const main = document.createElement("div"); main.className = "node-main";
    const name = document.createElement("div"); name.className = "node-name"; name.textContent = node.name;
    const meta = document.createElement("div"); meta.className = "node-sub"; meta.textContent = `${node.address}:${node.port} · ${node.subscriptionName}`;
    const badge = document.createElement("span"); badge.className = "node-badge"; badge.textContent = node.selected ? "ВЫБРАН" : node.protocol.toUpperCase();
    main.append(name, meta); row.append(main, badge);
    row.addEventListener("click", async () => {
      try {
        await api(`/api/nodes/${encodeURIComponent(node.id)}/select`, { method: "POST" }); await refresh();
        if (ui.vpnEnabled) notice("Сервер применён. Проверяем соединение.");
      }
      catch (error) { notice(error.message, true); }
    });
    container.append(row);
  }
}

function renderRouting() {
  const row = $("#routing-profile");
  const rulesExpanded = row.querySelector(".profile-rules-details")?.open || false;
  row.replaceChildren();
  if (!ui.routing) {
    const label = document.createElement("span"); label.className = "muted"; label.textContent = "Профиль Happ не добавлен. Доступен встроенный геоблок."; row.append(label); return;
  }
  const details = document.createElement("div");
  const title = document.createElement("div"); title.className = "profile-name"; title.textContent = ui.routing.name;
  const groups = [
    ["Через VPN", [...(ui.routing.proxyDomains || []), ...(ui.routing.proxyIPs || [])]],
    ["Напрямую", [...(ui.routing.directDomains || []), ...(ui.routing.directIPs || [])]],
    ["Блокировать", [...(ui.routing.blockDomains || []), ...(ui.routing.blockIPs || [])]],
  ];
  const ruleCount = groups.reduce((total, [, rules]) => total + rules.length, 0);
  const fallback = ui.routing.globalProxy ? "остальной трафик → VPN" : "остальной трафик → напрямую";
  const order = (ui.routing.routeOrder || ["block", "proxy", "direct"]).join(" → ");
  const meta = document.createElement("div"); meta.className = "profile-meta";
  const active = ["happ", "geoblock"].includes(ui.displayMode) || ui.devicePolicies.some(policy => ["happ", "geoblock"].includes(policy.mode));
  meta.textContent = `${active ? "Используется в правилах" : "Сохранён, не активен"} · ${ruleCount} правил · ${fallback} · порядок ${order}`;
  details.append(title, meta);
  const ruleDetails = document.createElement("details"); ruleDetails.className = "profile-rules-details";
  ruleDetails.open = rulesExpanded;
  const summary = document.createElement("summary"); summary.textContent = "Показать правила"; ruleDetails.append(summary);
  const list = document.createElement("div"); list.className = "profile-rules";
  let hasRules = false;
  for (const [label, rules] of groups) {
    if (!rules.length) continue;
    hasRules = true;
    const section = document.createElement("section"); section.className = "profile-rule-group";
    const heading = document.createElement("strong"); heading.textContent = `${label} · ${rules.length}`;
    const entries = document.createElement("div"); entries.className = "profile-rule-items";
    for (const rule of rules.slice(0, 30)) {
      const item = document.createElement("code"); item.textContent = rule; entries.append(item);
    }
    if (rules.length > 30) {
      const more = document.createElement("span"); more.className = "muted"; more.textContent = `и ещё ${rules.length - 30}`; entries.append(more);
    }
    section.append(heading, entries); list.append(section);
  }
  if (!hasRules) {
    const empty = document.createElement("span"); empty.className = "muted"; empty.textContent = "Отдельных списков нет — используется только правило для остального трафика."; list.append(empty);
  }
  ruleDetails.append(list); details.append(ruleDetails);
  const remove = document.createElement("button"); remove.className = "remove-profile"; remove.textContent = "Убрать профиль";
  remove.addEventListener("click", async () => {
    try {
      await api(`/api/routing/${encodeURIComponent(ui.routing.id)}`, { method: "DELETE" }); await refresh();
      notice(ui.connected ? "Профиль убран и изменения применены к VPN." : "Профиль убран. Изменение применится при подключении VPN.");
    }
    catch (error) { notice(error.message, true); }
  });
  row.append(details, remove);
}

async function refresh() {
  await loadStatus();
  await Promise.all([loadSubscriptions(), loadNodes(), loadDevices()]);
}

$("#subscription-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = event.submitter; button.disabled = true;
  try {
    await api("/api/subscriptions", { method: "POST", body: JSON.stringify({ url: $("#subscription-url").value.trim(), name: $("#subscription-name").value.trim() }) });
    $("#subscription-url").value = ""; $("#subscription-name").value = "";
    await refresh(); notice("Подписка добавлена.");
  } catch (error) { notice(error.message, true); }
  finally { button.disabled = false; }
});

$("#routing-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = event.submitter; button.disabled = true;
  try {
    const result = await api("/api/routing/import", { method: "POST", body: JSON.stringify({ link: $("#routing-link").value.trim() }) });
    $("#routing-link").value = ""; trafficDirty = false; await loadStatus();
    notice(result.applied ? "Правила импортированы и применены к VPN." : "Правила импортированы. Они применятся при подключении VPN.");
  } catch (error) { notice(error.message, true); }
  finally { button.disabled = false; }
});

$("#traffic-mode").addEventListener("change", () => { trafficDirty = true; });
function addManualDevice(kind) {
  const input = kind === "mac" ? $("#manual-device-mac") : $("#manual-device-ip");
  const value = input.value.trim().toLowerCase();
  if (!value) { notice("Укажите MAC или IP устройства.", true); return; }
  const key = kind === "mac" ? "mac" : "ip";
  const duplicate = [...document.querySelectorAll("#device-picker .device-option")].some(row => row.dataset[key] === value);
  if (duplicate) { notice("Это устройство уже есть в списке. Выберите для него режим.", true); return; }
  $("#device-picker .muted")?.remove();
  $("#device-picker").append(createDeviceRow({ [key]: value, mode: "geoblock" }, `${value} · вручную`, true));
  input.value = "";
  trafficDirty = true;
}
$("#add-device-mac").addEventListener("click", () => addManualDevice("mac"));
$("#add-device-ip").addEventListener("click", () => addManualDevice("ip"));
$("#traffic-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = event.submitter;
  button.disabled = true;
  const devicePolicies = [...document.querySelectorAll("#device-picker .device-option")].flatMap(row => {
    const mode = row.querySelector(".device-policy-mode").value;
    return mode ? [{ ...(row.dataset.mac ? { mac: row.dataset.mac } : { ip: row.dataset.ip }), mode }] : [];
  });
  try {
    const result = await api("/api/traffic", { method: "PUT", body: JSON.stringify({ mode: $("#traffic-mode").value, deviceIPs: [], deviceMACs: [], devicePolicies }) });
    trafficDirty = false;
    await refresh();
    notice(result.applied ? "Режим трафика применён к VPN." : "Режим сохранён и применится при подключении VPN.");
  } catch (error) { notice(error.message, true); }
  finally { button.disabled = false; }
});

$("#connect").addEventListener("click", async (event) => {
  event.currentTarget.disabled = true;
  try { await api("/api/vpn/connect", { method: "POST" }); await loadStatus(); notice("VPN подключён."); }
  catch (error) { notice(error.message, true); await loadStatus().catch(() => {}); }
});

$("#disconnect").addEventListener("click", async (event) => {
  event.currentTarget.disabled = true;
  try { await api("/api/vpn/disconnect", { method: "POST" }); await loadStatus(); notice("VPN отключён."); }
  catch (error) { notice(error.message, true); }
});

$("#auto-connect").addEventListener("click", async (event) => {
  const button = event.currentTarget;
  button.disabled = true;
  if (ui.autoMode) {
    try {
      await api("/api/vpn/auto-disable", { method: "POST" });
      await loadStatus();
      notice("Автомониторинг остановлен. Подключение к текущему серверу сохранено.");
    } catch (error) { notice(error.message, true); }
    finally { button.disabled = ui.nodes.length === 0; button.textContent = ui.autoMode ? "Отключить авто" : "Авто · лучший сервер"; }
    return;
  }
  button.textContent = "Проверяю серверы…";
  try {
    const result = await api("/api/vpn/auto-connect", { method: "POST" });
    await refresh();
    if (result.pending) {
      notice("Авто включён. Сейчас нет сервера, прошедшего HTTP-проверку; клиент продолжит поиск и подключится сам, когда найдёт рабочий.");
    } else {
      notice(`Авто включён: ${result.selectedNode.name}, отклик ${result.latencyMs} мс. GET-проверка идёт каждые 10 секунд; при двух сбоях сервер сменится автоматически.`);
    }
  } catch (error) { notice(error.message, true); }
  finally { button.disabled = ui.nodes.length === 0; button.textContent = ui.autoMode ? "Отключить авто" : "Авто · лучший сервер"; }
});

refresh().catch((error) => notice(error.message, true));
setInterval(() => loadStatus().catch(() => {}), 5000);
setInterval(() => refresh().catch(() => {}), 30000);

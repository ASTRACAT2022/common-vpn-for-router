const $ = (selector) => document.querySelector(selector);
const ui = { connected: false, autoMode: false, selectedNode: null, nodes: [], routing: null };

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
  $("#hero-description").textContent = ui.autoMode
    ? `${ui.selectedNode?.name || "Поиск рабочего сервера"} · авто проверяет VPN и переключит сервер при сбое.`
    : ui.connected
      ? `${ui.selectedNode?.name || "Сервер выбран"} · VPN ${ui.routing ? "с правилами маршрутизации" : "для всего трафика"}`
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
  const routingData = await api("/api/routing");
  ui.routing = routingData.profile;
  renderRouting();
  updateConnection();
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
    row.append(details, update); container.append(row);
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
    const label = document.createElement("span"); label.className = "muted"; label.textContent = "Профиль не добавлен"; row.append(label); return;
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
  meta.textContent = `${ruleCount} правил · ${fallback} · порядок ${order}`;
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
  await Promise.all([loadStatus(), loadSubscriptions(), loadNodes()]);
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
    $("#routing-link").value = ""; await loadStatus();
    notice(result.applied ? "Правила импортированы и применены к VPN." : "Правила импортированы. Они применятся при подключении VPN.");
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

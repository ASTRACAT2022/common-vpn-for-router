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
  notice.timer = setTimeout(() => { element.hidden = true; }, 6500);
}

function updateConnection() {
  const pill = $("#connection-pill");
  pill.classList.toggle("connected", ui.connected);
  pill.classList.toggle("disconnected", !ui.connected);
  $("#connection-label").textContent = ui.connected ? (ui.autoMode ? "АВТО · ONLINE" : "ONLINE") : (ui.autoMode ? "АВТО · ПОИСК" : "OFFLINE");
  $("#hero-title").textContent = ui.connected ? "VPN подключён" : (ui.autoMode ? "Авто восстанавливает VPN" : "Ваш VPN готов");
  $("#hero-description").textContent = ui.autoMode
    ? `${ui.selectedNode?.name || "Поиск рабочего сервера"} · авто проверяет VPN и переключит сервер при сбое.`
    : ui.connected
      ? `${ui.selectedNode?.name || "Сервер выбран"} · VPN ${ui.routing ? "с правилами маршрутизации" : "для всего трафика"}`
      : (ui.selectedNode ? `Выбран сервер: ${ui.selectedNode.name}` : "Добавьте ссылку на подписку и выберите сервер.");
  $("#connect").textContent = ui.connected ? "Применить изменения" : "Подключиться";
  $("#connect").disabled = !ui.selectedNode;
  $("#disconnect").disabled = !ui.connected;
  $("#auto-connect").disabled = ui.nodes.length === 0;
  $("#auto-connect").textContent = ui.autoMode ? "Отключить авто" : "Авто · лучший сервер";
}

async function loadStatus() {
  const status = await api("/api/status");
  ui.connected = status.connected;
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
    const meta = document.createElement("div"); meta.className = "subscription-meta"; meta.textContent = `${sub.nodeCount} серверов`;
    details.append(title, meta); row.append(details); container.append(row);
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
        if (ui.connected) notice("Сервер выбран. Нажмите «Применить изменения», чтобы переключить VPN.");
      }
      catch (error) { notice(error.message, true); }
    });
    container.append(row);
  }
}

function renderRouting() {
  const row = $("#routing-profile"); row.replaceChildren();
  if (!ui.routing) {
    const label = document.createElement("span"); label.className = "muted"; label.textContent = "Профиль не добавлен"; row.append(label); return;
  }
  const details = document.createElement("div");
  const title = document.createElement("div"); title.className = "profile-name"; title.textContent = ui.routing.name;
  const meta = document.createElement("div"); meta.className = "profile-meta"; meta.textContent = ui.routing.globalProxy ? "Нераспределённый трафик → VPN" : "Нераспределённый трафик → напрямую";
  details.append(title, meta);
  const remove = document.createElement("button"); remove.className = "remove-profile"; remove.textContent = "Убрать профиль";
  remove.addEventListener("click", async () => {
    try {
      await api(`/api/routing/${encodeURIComponent(ui.routing.id)}`, { method: "DELETE" }); await refresh();
      notice(ui.connected ? "Профиль убран. Нажмите «Применить изменения», чтобы обновить VPN." : "Профиль убран. Изменение применится при подключении VPN.");
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
    await api("/api/routing/import", { method: "POST", body: JSON.stringify({ link: $("#routing-link").value.trim() }) });
    $("#routing-link").value = ""; await loadStatus();
    notice(ui.connected ? "Правила импортированы. Нажмите «Применить изменения», чтобы обновить VPN." : "Правила импортированы. Они применятся при подключении VPN.");
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
      notice("Авто включён. Сейчас нет сервера, прошедшего HTTPS-проверку; клиент продолжит поиск и подключится сам, когда найдёт рабочий.");
    } else {
      notice(`Авто включён: ${result.selectedNode.name}, отклик ${result.latencyMs} мс. GET-проверка идёт каждые 10 секунд; при двух сбоях сервер сменится автоматически.`);
    }
  } catch (error) { notice(error.message, true); }
  finally { button.disabled = ui.nodes.length === 0; button.textContent = ui.autoMode ? "Отключить авто" : "Авто · лучший сервер"; }
});

refresh().catch((error) => notice(error.message, true));
setInterval(() => loadStatus().catch(() => {}), 5000);

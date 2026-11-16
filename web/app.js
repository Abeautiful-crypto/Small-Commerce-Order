/* 衡序交易 · 订单交易系统前端
 * 视觉与信息架构来自「优化C端与B端原型」，所有数据调用后端真实接口。
 * 金额单位：分（int），前端仅做展示换算，判定与计算全部在服务端。
 *
 * 双端部署：C 端（买家购物页）与 B 端（工作台）由后端两个端口分别托管，
 * 页面入口各自加载本脚本并通过 window.APP_SIDE 声明自己属于哪一端。
 * 登录接口会按端校验角色，因此 C 端不可能登录成仓库管理员。
 */
const SIDE = window.APP_SIDE === 'b' ? 'b' : 'c';
const SIDE_LABEL = SIDE === 'c' ? 'C 端 · 买家' : 'B 端 · 工作台';
const SIDE_ROLES = SIDE === 'c' ? ['buyer'] : ['ops', 'warehouse', 'cs'];
const ROLES = {
  buyer: { label: '买家', name: 'C 端 · 买家', ico: '🛒', desc: '逛店、下单、支付、取消、申请退款（仅自己的订单）' },
  ops: { label: '运营', name: 'B 端 · 工作台', ico: '📊', desc: '商品管理、订单核查、资金流水（不能改订单状态）' },
  warehouse: { label: '仓库', name: 'B 端 · 工作台', ico: '📦', desc: '查待发货、登记发货（一次性，退款中不可发货）' },
  cs: { label: '客服', name: 'B 端 · 工作台', ico: '🎧', desc: '审核退款、渠道确认、处理迟到支付异常' },
};
const ROLE_META = {
  ops: { name: '林舟', label: '运营专员', icon: 'chart' },
  warehouse: { name: '周师傅', label: '仓库管理员', icon: 'box' },
  cs: { name: '陈禾', label: '售后客服', icon: 'headset' },
  buyer: { name: '张小买', label: '买家', icon: 'user' },
};
const STATUS = {
  PENDING_PAY: { label: '待付款' },
  PENDING_SHIP: { label: '待发货' },
  REFUNDING: { label: '退款审核中' },
  SHIPPED: { label: '已发货' },
  CLOSED: { label: '已关闭' },
  REFUNDED: { label: '已退款' },
  ABNORMAL_PAY: { label: '异常支付' },
};
const REFUND_STATUS = {
  PENDING: { label: '待客服审核' },
  AWAITING: { label: '等待渠道确认' },
  FAILED: { label: '渠道失败·可重试' },
  SUCCEEDED: { label: '退款成功' },
  REJECTED: { label: '已拒绝' },
};
const ABNORMAL_STATUS = { PENDING: '待处理', HANDLED: '已退款' };
const STATUS_ORDER = ['PENDING_PAY', 'PENDING_SHIP', 'REFUNDING', 'SHIPPED', 'CLOSED', 'REFUNDED', 'ABNORMAL_PAY'];
const FALLBACK_USERS = [
  { id: 'u001', name: '张小买', role: 'buyer' }, { id: 'u002', name: '李小购', role: 'buyer' },
  { id: 'u003', name: '王小单', role: 'buyer' }, { id: 'admin', name: '运营专员', role: 'ops' },
  { id: 'wh', name: '仓库管理员', role: 'warehouse' }, { id: 'cs', name: '客服小花', role: 'cs' },
];
const PERMISSIONS = {
  ops: ['商品上下架', '库存查看', '交易汇总', '全链路对账'],
  warehouse: ['待发货查看', '登记发货'],
  cs: ['退款审核', '异常支付处理'],
  buyer: ['浏览商品', '下单支付', '取消订单', '申请退款'],
};

let current = null;      // { role, id, name, token }
let meta = null;
let buyerTab = 'shop';
let bizPage = '概览';
let showRoleMenu = false;
let orderFilter = 'all';
let auditFilter = '全部';
let filterOpen = false;
let shopQuery = '';
let productQuery = '';
let busy = false;
let cdReloaded = false;
let db = { products: [], orders: [], refunds: [], abnormal: [], ledger: [], shipments: [], payments: [], notifyResults: [], summary: null };

const $ = (s) => document.querySelector(s);
const formatYuan = (f) => (Number(f || 0) / 100).toFixed(2);
const esc = (v) => String(v == null ? '' : v).replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const stOf = (s) => STATUS[s] || { label: s || '-' };
function usersOf(role) {
  const list = (meta && meta.users) || FALLBACK_USERS;
  return list.filter((u) => u.role === role);
}
function fmtTime(t) {
  if (!t) return '-';
  const d = new Date(t);
  if (isNaN(d.getTime())) return String(t);
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
function fmtRelTime(t) {
  if (!t) return '-';
  const d = new Date(t);
  if (isNaN(d.getTime())) return String(t);
  const p = (n) => String(n).padStart(2, '0');
  const hm = `${p(d.getHours())}:${p(d.getMinutes())}`;
  const now = new Date();
  const sameDay = (a, b) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate();
  const yest = new Date(now.getTime() - 864e5);
  if (sameDay(d, now)) return `今天 ${hm}`;
  if (sameDay(d, yest)) return `昨天 ${hm}`;
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${hm}`;
}
function fmtClock(ms) {
  if (ms < 0) ms = 0;
  const s = Math.floor(ms / 1000);
  return `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`;
}
function badge(status) {
  const s = stOf(status);
  return `<span class="status status-${esc(s.label)}">${esc(s.label)}</span>`;
}

/* ================= SVG 图标 ================= */
function icon(name, size = 20) {
  const paths = {
    logo: '<path d="M5 7.5 12 3l7 4.5v9L12 21l-7-4.5z"/><path d="m8.5 10 3.5-2 3.5 2v4L12 16l-3.5-2z"/>',
    home: '<path d="m3 11 9-8 9 8"/><path d="M5 10v10h14V10M9 20v-6h6v6"/>',
    order: '<path d="M6 3h12v18H6z"/><path d="M9 7h6M9 11h6M9 15h4"/>',
    user: '<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>',
    search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-4-4"/>',
    bag: '<path d="M5 8h14l-1 13H6z"/><path d="M9 8a3 3 0 0 1 6 0"/>',
    chevron: '<path d="m9 18 6-6-6-6"/>',
    arrow: '<path d="m5 12 14 0m-5-5 5 5-5 5"/>',
    close: '<path d="m6 6 12 12M18 6 6 18"/>',
    check: '<path d="m5 12 4 4L19 6"/>',
    grid: '<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>',
    box: '<path d="m3 7 9 5 9-5-9-4z"/><path d="M3 7v10l9 5 9-5V7M12 12v10"/>',
    headset: '<path d="M4 14v-2a8 8 0 0 1 16 0v2"/><path d="M4 14h3v6H5a1 1 0 0 1-1-1zM20 14h-3v6h2a1 1 0 0 0 1-1zM17 20c0 1-2 2-4 2"/>',
    chart: '<path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/>',
    shield: '<path d="M12 3 4 6v6c0 5 3.5 8 8 10 4.5-2 8-5 8-10V6z"/><path d="m9 12 2 2 4-5"/>',
    bell: '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9"/><path d="M10 21h4"/>',
    logout: '<path d="M10 4H4v16h6M14 8l4 4-4 4M8 12h10"/>',
    lock: '<rect x="4" y="10" width="16" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/>',
    truck: '<path d="M3 6h11v11H3zM14 10h4l3 3v4h-7z"/><circle cx="7" cy="19" r="2"/><circle cx="18" cy="19" r="2"/>',
    wallet: '<path d="M3 6h16v14H3z"/><path d="M3 8V5h13M15 12h6v5h-6z"/>',
    refresh: '<path d="M20 7h-5V2"/><path d="M20 7a9 9 0 1 0 1 8"/>',
    alert: '<path d="m12 3 10 18H2z"/><path d="M12 9v5M12 18h.01"/>',
    filter: '<path d="M4 5h16l-6 7v6l-4 2v-8z"/>',
  };
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] || ''}</svg>`;
}

/* ================= Toast / 弹层 ================= */
const TOAST_MS = 3000;
function toast(msg, ms) {
  const t = $('#toast');
  t.innerHTML = `${icon('check', 16)}<span>${esc(msg)}</span>`;
  t.hidden = false;
  clearTimeout(t._t);
  t._t = setTimeout(() => {
    t.hidden = true;
    t.innerHTML = '';
  }, ms || TOAST_MS);
}
function openModal(html) {
  closeModal();
  const m = document.createElement('div');
  m.className = 'modal-backdrop';
  m.id = 'modalRoot';
  m.innerHTML = html;
  m.addEventListener('mousedown', (e) => { if (e.target === m) closeModal(); });
  document.body.appendChild(m);
}
function closeModal() { document.querySelectorAll('.modal-backdrop').forEach((m) => m.remove()); }
function modalFrame(title, ico, body) {
  return `<div class="detail-modal">
    <button class="modal-close" onclick="closeModal()" aria-label="关闭">${icon('close')}</button>
    <div class="detail-title"><span class="detail-icon">${icon(ico)}</span><div><h3>${esc(title)}</h3></div></div>
    ${body}
  </div>`;
}
function confirmAction(title, note, confirmLabel, js) {
  openModal(modalFrame(title, 'alert', `
    <div class="form-note" style="margin-top:16px">${note}</div>
    <button class="primary wide" onclick="${js}">${esc(confirmLabel)}</button>
    <button class="secondary wide ghost" onclick="closeModal()">取消</button>`));
}

/* ================= API + 前端权限守卫 ================= */
const ROUTE_GUARD = [
  { re: /^\/api\/orders$/, roles: ['buyer'], action: '下单/支付' },
  { re: /^\/api\/orders\/[^/]+\/(cancel|expire|pay-intent|refund-apply)$/, roles: ['buyer'], action: '下单/支付/取消/申请退款' },
  { re: /^\/api\/orders\/[^/]+\/ship$/, roles: ['warehouse'], action: '登记发货' },
  { re: /^\/api\/products$/, roles: ['ops'], action: '商品管理' },
  { re: /^\/api\/products\/[^/]+\/onsale$/, roles: ['ops'], action: '商品上下架' },
  { re: /^\/api\/refunds\/[^/]+\/(review|retry|channel-notify)$/, roles: ['cs'], action: '审核退款/处理异常支付' },
  { re: /^\/api\/abnormal\/[^/]+\/handle$/, roles: ['cs'], action: '处理异常支付' },
  { re: /^\/api\/reset$/, roles: ['ops'], action: '重置演示数据' },
  { re: /^\/api\/payments\/notify$/, roles: ['buyer'], action: '下单/支付' },
];
function requireRole(roles, action) {
  if (!current || !roles.includes(current.role)) { denyGuard(action); return false; }
  return true;
}
function denyGuard(action) {
  openModal(`<div class="detail-modal"><div class="deny">
    <div class="ic">⛔</div><div class="t1">无权访问（前端权限守卫）</div>
    <div class="t2">当前身份：<b>${current ? esc(ROLES[current.role].label + ' / ' + current.name) : '未登录'}</b><br/>
    操作「${esc(action)}」需要其它角色权限，<b>本次请求未发出，未做任何数据改动</b>。</div>
    <button class="primary wide" onclick="closeModal()">我知道了</button></div></div>`);
}
async function api(path, opts) {
  const o = opts || {};
  if (!o.skipGuard && o.method && o.method !== 'GET') {
    const rule = ROUTE_GUARD.find((g) => g.re.test(path));
    if (rule && !requireRole(rule.roles, rule.action)) {
      const err = new Error('前端权限守卫已拦截，未发起请求');
      err.guarded = true;
      throw err;
    }
  }
  const headers = { 'Content-Type': 'application/json', 'X-Client': SIDE };
  if (current) {
    headers['X-Role'] = current.role; headers['X-User-Id'] = current.id;
    if (current.token) headers['X-Token'] = current.token;
  }
  const res = await fetch(path, {
    method: o.method || 'GET', headers,
    body: o.body === undefined ? undefined : JSON.stringify(o.body),
  });
  let json;
  try { json = await res.json(); } catch (e) { json = { ok: false, error: { message: '服务端返回异常' } }; }
  if (!res.ok || json.ok === false) {
    const err = new Error((json.error && json.error.message) || ('HTTP ' + res.status));
    err.code = json.error && json.error.code;
    err.status = res.status;
    throw err;
  }
  return json.data;
}
async function act(fn, okMsg) {
  if (busy) { toast('操作进行中，请勿重复点击'); return null; }
  busy = true;
  try {
    const data = await fn();
    closeModal();
    if (okMsg) toast(okMsg);
    await refresh(); render();
    return data;
  } catch (e) {
    toast(e.guarded ? e.message : '操作被拒绝：' + e.message, 3200);
    await refresh(); render();
    return null;
  } finally { busy = false; }
}
async function refresh() {
  if (!current) return;
  if (current.role === 'buyer') {
    const [products, orders] = await Promise.all([api('/api/products'), api('/api/orders')]);
    db.products = products || []; db.orders = orders || [];
  } else if (current.role === 'ops') {
    const [st, summary] = await Promise.all([api('/api/state'), api('/api/summary')]);
    db.products = st.products || []; db.orders = st.orders || []; db.ledger = st.ledger || [];
    db.notifyResults = st.notifyResults || []; db.refunds = st.refunds || [];
    db.abnormal = st.abnormalPayments || []; db.shipments = st.shipments || [];
    db.payments = st.payments || []; db.summary = summary;
  } else if (current.role === 'warehouse') {
    const [orders, shipments] = await Promise.all([api('/api/orders'), api('/api/shipments')]);
    db.orders = orders || []; db.shipments = shipments || [];
  } else if (current.role === 'cs') {
    const [refunds, abnormal, orders] = await Promise.all([api('/api/refunds'), api('/api/abnormal'), api('/api/orders')]);
    db.refunds = refunds || []; db.abnormal = abnormal || []; db.orders = orders || [];
  }
}

/* ================= 登录 ================= */
function renderLogin() {
  let selRole = SIDE_ROLES[0];
  const pwdless = !!(meta && meta.loginPasswordless);
  const pwdField = pwdless ? '' : `
    <div class="login-field"><label>登录密码</label>
      <input id="pwdInput" type="password" autocomplete="current-password"
        placeholder="${(meta && meta.env === 'production') ? '生产模式：请输入账号密码' : '演示模式可留空'}"/></div>`;
  const modeNote = pwdless
    ? '<b>免密码登录</b>：选择角色与账号即可进入，登录态仍绑定服务端会话令牌。'
    : ((meta && meta.env === 'production')
      ? '<b>生产模式</b>：密码由服务端环境变量配置，登录态仅认会话令牌。'
      : '演示模式：选角色即登录，密码可留空。');
  const hint = SIDE === 'c'
    ? `当前是 <b>C 端买家页面</b>（独立端口）。此处只接受买家账号，运营 / 仓库 / 客服请打开 <b>B 端工作台</b> 页面登录；用工作台账号在这里登录会被服务端拒绝。`
    : `当前是 <b>B 端工作台</b>（独立端口）。此处只接受运营 / 仓库 / 客服账号，买家请打开 <b>C 端买家页面</b> 登录；用买家账号在这里登录会被服务端拒绝。`;
  $('#app').innerHTML = `
  <div class="login-page"><div class="login-card">
    <div class="login-brand">
      <span class="brand-mark">${icon('logo', 24)}</span>
      <div><h1>衡序交易</h1><small>小型电商订单交易系统 · ${SIDE_LABEL}</small></div>
    </div>
    <div class="login-hint">${hint}</div>
    <div class="role-grid side-${SIDE}" id="roleGrid"></div>
    <div class="login-field"><label>选择演示账号</label><select id="userSel"></select></div>
    ${pwdField}
    <button class="enter-btn" onclick="doLogin()">进 入 系 统</button>
    <div class="login-subtle">${modeNote} 金额单位统一为「分」。</div>
  </div></div>`;
  const rg = $('#roleGrid');
  SIDE_ROLES.forEach((k) => {
    const v = ROLES[k];
    const d = document.createElement('div');
    d.className = 'role-card' + (k === selRole ? ' sel' : '');
    d.innerHTML = `<div class="ico">${v.ico}</div><div class="nm">${v.label}</div><div class="ds">${v.desc}</div>`;
    d.onclick = () => {
      selRole = k;
      document.querySelectorAll('.role-card').forEach((x) => x.classList.remove('sel'));
      d.classList.add('sel');
      fillUsers(k);
    };
    rg.appendChild(d);
  });
  window._selRole = selRole;
  fillUsers(selRole);
}
function fillUsers(role) {
  window._selRole = role;
  const s = $('#userSel');
  s.innerHTML = '';
  usersOf(role).forEach((u) => {
    const o = document.createElement('option');
    o.value = u.id;
    o.textContent = `${u.name}(${u.id})`;
    s.appendChild(o);
  });
}
async function loginAs(role, userId, password) {
  if (!SIDE_ROLES.includes(role)) {
    throw new Error(`当前页面是 ${SIDE_LABEL}，不接受 ${ROLES[role] ? ROLES[role].label : role} 账号`);
  }
  let token = '';
  try {
    // side 与页面端一致：后端会再校验一次角色所属端，双重保险
    const sess = await api('/api/session', { method: 'POST', body: { role, userId, password, side: SIDE } });
    if (sess && sess.sessionEnabled && sess.token) token = sess.token;
  } catch (e) {
    if (meta && meta.env === 'production') throw e;
    if (e.status === 403) throw e; // 端/角色不匹配必须直接失败，不能降级
    // demo 模式下 Redis 不可用时降级为请求头身份
  }
  const u = usersOf(role).find((x) => x.id === userId);
  current = { role, id: userId, name: u ? u.name : userId, token };
  db = { products: [], orders: [], refunds: [], abnormal: [], ledger: [], shipments: [], payments: [], notifyResults: [], summary: null };
  buyerTab = 'shop';
  bizPage = role === 'ops' ? '概览' : role === 'warehouse' ? '发货管理' : role === 'cs' ? '退款审核' : '概览';
  orderFilter = 'all'; auditFilter = '全部'; filterOpen = false; shopQuery = ''; productQuery = ''; cdReloaded = false;
  await refresh();
  render();
  toast(`已以 ${ROLES[role].label}·${current.name} 身份登录${token ? '（Redis 会话）' : ''}`);
}
function doLogin() {
  const id = $('#userSel').value;
  const role = window._selRole;
  const u = usersOf(role).find((x) => x.id === id);
  if (!u) { toast('请选择有效账号'); return; }
  const password = ($('#pwdInput') && $('#pwdInput').value) || '';
  (async () => {
    try { await loginAs(role, id, password); }
    catch (e) { toast('登录失败：' + e.message, 3200); }
  })();
}
function doLogout() { current = null; closeModal(); renderLogin(); }

/* ================= 主框架 ================= */
function render() {
  if (!current) { renderLogin(); return; }
  // 端与角色不一致（例如手工改了 localStorage / 跨端带令牌）时强制退回登录
  if (!SIDE_ROLES.includes(current.role)) {
    const bad = ROLES[current.role] ? ROLES[current.role].label : current.role;
    current = null;
    renderLogin();
    toast(`${bad} 账号不属于 ${SIDE_LABEL}，已退出登录`, 5000);
    return;
  }
  const note = SIDE === 'c' ? '买家中心 · 仅本人订单' : '交易工作台 · ' + esc(ROLES[current.role].label) + '权限';
  $('#app').innerHTML = `
  <div class="app-shell">
    <header class="top-switcher">
      <div class="brand"><span class="brand-mark">${icon('logo', 22)}</span><span>衡序交易</span></div>
      <div class="side-pill side-${SIDE}">${SIDE_LABEL}${location.port ? ' · :' + esc(location.port) : ''}</div>
      <div class="prototype-note"><span></span> ${note}</div>
    </header>
    ${SIDE === 'c' ? renderCustomer() : renderBusiness()}
  </div>`;
}

/* ================= C 端：逛逛 ================= */
function renderCustomer() {
  const orders = db.orders;
  return `
  <main class="customer-page"><div class="customer-frame">
    <div class="mobile-header">
      <div><span class="eyebrow">GOOD MORNING</span><h2>${esc(current.name)}，今天想买点什么？</h2></div>
      <button class="round-button" onclick="toast('暂无新消息')">${icon('bell')}</button>
    </div>
    ${buyerTab === 'shop' ? renderShop() : ''}
    ${buyerTab === 'orders' ? renderBuyerOrders() : ''}
    ${buyerTab === 'profile' ? renderProfile() : ''}
    <nav class="mobile-nav">
      <button class="${buyerTab === 'shop' ? 'active' : ''}" onclick="switchTab('shop')">${icon('home')}<span>逛逛</span></button>
      <button class="${buyerTab === 'orders' ? 'active' : ''}" onclick="switchTab('orders')">${icon('order')}<span>订单</span>${orders.some((o) => o.status === 'PENDING_PAY') ? '<i></i>' : ''}</button>
      <button class="${buyerTab === 'profile' ? 'active' : ''}" onclick="switchTab('profile')">${icon('user')}<span>我的</span></button>
    </nav>
  </div></main>`;
}
function switchTab(t) { buyerTab = t; render(); }
function renderShop() {
  const q = shopQuery.trim().toLowerCase();
  const list = db.products.filter((p) => !q || p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q));
  const hero = db.products[0] || { image: 'images/product1.png', available: 0 };
  return `
    <div class="search-box">${icon('search', 19)}
      <input id="shopSearch" value="${esc(shopQuery)}" placeholder="搜索好物" aria-label="搜索商品"
        oninput="shopQuery=this.value;renderShopGrid()"/>
      <kbd>搜索</kbd>
    </div>
    <section class="customer-hero">
      <div class="hero-copy">
        <span>限时好物计划</span>
        <h1>认真挑选，<br/>简单抵达。</h1>
        <p>每件商品都经过精选，安心交易有迹可循。</p>
        <button onclick="document.getElementById('products')?.scrollIntoView({behavior:'smooth'})">立即选购 ${icon('arrow', 17)}</button>
      </div>
      <div class="hero-visual"><img src="${esc(hero.image)}" alt="精选商品"/><span class="floating-tag">限量热卖</span></div>
    </section>
    <section class="trust-strip">
      <div>${icon('shield')}<span><b>支付保障</b><small>渠道校验，重复不扣款</small></span></div>
      <div>${icon('refresh')}<span><b>售前可退</b><small>未发货支持整单退款</small></span></div>
      <div>${icon('truck')}<span><b>履约可查</b><small>发货节点全程留痕</small></span></div>
    </section>
    <section class="products-section" id="products">
      <div class="section-heading">
        <div><span class="eyebrow">CURATED FOR YOU</span><h2>本周精选</h2></div>
        <button onclick="toast('已在售商品共 ' + (${db.products.length}) + ' 件')">查看全部 ${icon('chevron', 15)}</button>
      </div>
      <div class="product-grid" id="shopGrid">${shopCards(list)}</div>
    </section>`;
}
function shopCards(list) {
  if (!list.length) return '<div class="empty-state">没有匹配的商品</div>';
  return list.map((p) => `
    <article class="product-card" key="${esc(p.id)}">
      <button class="product-image" onclick="openBuySheet('${esc(p.id)}')" ${p.onSale && p.available > 0 ? '' : 'disabled'}>
        <img src="${esc(p.image)}" alt="${esc(p.name)}"/>
        <span>${p.onSale ? (p.available > 0 ? `仅剩 ${p.available} 件` : '暂时售罄') : '已下架'}</span>
      </button>
      <div class="product-info">
        <p class="product-code">${esc(p.id)}</p>
        <h3>${esc(p.name)}</h3>
        <p>已售 ${p.sales} 件</p>
        <p class="old-price">¥${formatYuan(p.oldPriceFen)}</p>
        <div class="product-bottom">
          <strong>¥${formatYuan(p.priceFen)}</strong>
          <button onclick="openBuySheet('${esc(p.id)}')" ${p.onSale && p.available > 0 ? '' : 'disabled'}>${icon('bag', 18)}</button>
        </div>
      </div>
    </article>`).join('');
}
function renderShopGrid() {
  const q = shopQuery.trim().toLowerCase();
  const list = db.products.filter((p) => !q || p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q));
  const g = $('#shopGrid');
  if (g) g.innerHTML = shopCards(list);
}
function openBuySheet(pid) {
  const p = db.products.find((x) => x.id === pid);
  if (!p) return;
  if (!p.onSale) { toast('商品已下架'); return; }
  window._buyPid = pid;
  window._buyQty = 1;
  openModal(`<div class="buy-sheet">
    <button class="modal-close" onclick="closeModal()" aria-label="关闭">${icon('close')}</button>
    <div class="sheet-product">
      <img src="${esc(p.image)}" alt=""/>
      <div><p>${esc(p.name)}</p><strong>¥${formatYuan(p.priceFen)}</strong><span>库存 ${p.available} 件 · 每单限购 2 件</span></div>
    </div>
    <div class="quantity-row"><span>购买数量</span>
      <div class="stepper">
        <button onclick="chgQty(-1)" aria-label="减少">−</button>
        <b id="qtyN">1</b>
        <button onclick="chgQty(1)" aria-label="增加">＋</button>
      </div>
    </div>
    <div class="rule-box">${icon('shield', 18)}<div><b>交易保障</b><p>下单即占库存，10 分钟内未支付将自动关闭并释放库存。</p></div></div>
    <div class="sheet-total"><span>应付</span><strong id="qtySum">¥${formatYuan(p.priceFen)}</strong></div>
    <button class="primary wide" onclick="submitOrder()">确认下单</button>
  </div>`);
}
function chgQty(d) {
  const p = db.products.find((x) => x.id === window._buyPid);
  if (!p) return;
  let n = window._buyQty + d;
  n = Math.max(1, Math.min(2, p.available, n));
  window._buyQty = n;
  $('#qtyN').textContent = n;
  $('#qtySum').textContent = '¥' + formatYuan(p.priceFen * n);
}
function submitOrder() {
  const pid = window._buyPid;
  const qty = window._buyQty || 1;
  (async () => {
    const o = await act(() => api('/api/orders', { method: 'POST', body: { productId: pid, qty } }));
    if (o) {
      buyerTab = 'orders';
      render();
      toast('下单成功，库存已为你保留 10 分钟');
    }
  })();
}

/* ================= C 端：我的订单 ================= */
function renderBuyerOrders() {
  const orders = db.orders;
  const filterDefs = [
    ['all', `全部 ${orders.length}`], ['PENDING_PAY', '待付款'],
    ['PENDING_SHIP', '待发货'], ['done', '已完成'],
  ];
  const visible = orders.filter((o) => {
    if (orderFilter === 'all') return true;
    if (orderFilter === 'done') return ['SHIPPED', 'CLOSED', 'REFUNDED'].includes(o.status);
    return o.status === orderFilter;
  });
  return `
  <section class="mobile-content">
    <div class="page-intro"><span class="eyebrow">MY ORDERS</span><h1>我的订单</h1><p>只有你本人可以查看和操作这些订单</p></div>
    <div class="order-filters">
      ${filterDefs.map(([k, label]) => `<button class="${orderFilter === k ? 'active' : ''}" onclick="orderFilter='${k}';render()">${esc(label)}</button>`).join('')}
    </div>
    <div class="buyer-orders">
      ${visible.length === 0 ? '<div class="empty-state">暂无该状态的订单</div>' : ''}
      ${visible.map(buyerOrderCard).join('')}
    </div>
  </section>`;
}
function buyerOrderCard(o) {
  const exp = o.status === 'PENDING_PAY' ? new Date(o.expiresAt).getTime() : 0;
  const left = exp - Date.now();
  let countdown = '';
  if (o.status === 'PENDING_PAY') {
    countdown = `<div class="countdown">${icon('alert', 16)} 请在 <b data-cd="${exp}">${fmtClock(left)}</b> 内完成支付，超时将释放库存</div>`;
  }
  const acts = [`<button class="text-button" onclick="openOrderDetail('${esc(o.id)}')">查看详情</button><span></span>`];
  if (o.status === 'PENDING_PAY') {
    acts.push(`<button class="secondary" onclick="cancelOrder('${esc(o.id)}')">取消订单</button>`);
    acts.push(`<button class="primary" onclick="payOrder('${esc(o.id)}')">立即支付</button>`);
    acts.push(`<button class="text-button" onclick="expireOrder('${esc(o.id)}')">模拟超时关闭</button>`);
  } else if (o.status === 'PENDING_SHIP') {
    acts.push(`<button class="secondary danger" onclick="applyRefund('${esc(o.id)}')">申请退款</button>`);
  } else if (o.status === 'SHIPPED') {
    acts.push(`<button class="secondary" onclick="openLogistics('${esc(o.id)}')">查看物流</button>`);
  } else if (o.status === 'CLOSED') {
    acts.push(`<button class="secondary" onclick="latePay('${esc(o.id)}')">模拟迟到支付</button>`);
  }
  return `
  <article class="buyer-order-card">
    <div class="order-card-head"><span>${esc(o.id)}</span>${badge(o.status)}</div>
    <button class="order-product" onclick="openOrderDetail('${esc(o.id)}')">
      <img src="${esc(o.productImage)}" alt=""/>
      <span><b>${esc(o.productName)}</b><small>¥${formatYuan(o.priceFen)} / 件 · 价格已快照</small><em>× ${o.qty}</em></span>
      <strong>¥${formatYuan(o.amountFen)}</strong>
    </button>
    ${countdown}
    <div class="order-actions">${acts.join('')}</div>
  </article>`;
}
function openOrderDetail(id) {
  const o = db.orders.find((x) => x.id === id);
  if (!o) { toast('订单不存在'); return; }
  const payDone = o.status !== 'PENDING_PAY';
  const shipDone = o.status === 'SHIPPED' || (!!o.shipmentId && (o.status === 'REFUNDED' || o.status === 'CLOSED'));
  const row = (k, v) => `<div class="detail-row"><span class="k">${k}</span><span class="v">${v}</span></div>`;
  openModal(modalFrame('订单详情', 'order', `
    <div class="timeline">
      <div class="done"><i>${icon('check', 14)}</i><div><b>订单已创建</b><span>${fmtRelTime(o.createdAt)} · 商品价格已快照</span></div></div>
      <div class="${payDone ? 'done' : ''}"><i>${payDone ? icon('check', 14) : ''}</i><div><b>支付校验</b><span>${payDone ? '金额与订单匹配，收款成功' : '等待付款'}</span></div></div>
      <div class="${shipDone ? 'done' : ''}"><i>${shipDone ? icon('check', 14) : ''}</i><div><b>仓库履约</b><span>${shipDone ? '已登记发货' : o.status === 'REFUNDING' ? '退款审核中，发货已冻结' : '等待合法状态后处理'}</span></div></div>
    </div>
    <div class="detail-rows">
      ${row('状态', badge(o.status))}
      ${row('商品', esc(o.productName))}
      ${row('单价快照', '¥' + formatYuan(o.priceFen))}
      ${row('数量 / 合计', o.qty + ' 件 / ¥' + formatYuan(o.amountFen))}
      ${row('付款截止', fmtTime(o.expiresAt))}
      ${row('渠道交易号', `<span class="mono">${esc(o.channelTxnId || '-')}</span>`)}
      ${row('退款单 / 发货记录', `<span class="mono">${esc(o.refundId || '-')} / ${esc(o.shipmentId || '-')}</span>`)}
    </div>`));
}
function openLogistics(id) {
  const o = db.orders.find((x) => x.id === id);
  if (!o) return;
  (async () => {
    let carrier = '顺丰速运';
    let track = o.shipmentId || ('SF' + o.id.slice(-9));
    try {
      const list = await api('/api/shipments');
      const sh = (list || []).find((x) => x.orderId === id);
      if (sh) { carrier = sh.carrier; track = sh.trackingNo || sh.id; }
    } catch (e) { /* 买家无权读全量发货记录，用订单上的发货单号展示 */ }
    openModal(modalFrame('物流轨迹', 'truck', `
      <div class="logistics-meta" style="margin-top:16px"><span>${esc(carrier)} · 已揽收</span><b>${esc(track)}</b></div>
      <div class="timeline">
        <div class="done"><i>${icon('check', 14)}</i><div><b>仓库已出库</b><span>商品完成拣货与交接</span></div></div>
        <div class="done"><i>${icon('check', 14)}</i><div><b>运输中</b><span>快件已到达转运中心</span></div></div>
        <div><i></i><div><b>派送中</b><span>预计明日送达，请保持手机畅通</span></div></div>
      </div>`));
  })();
}
function payOrder(id) {
  (async () => {
    try {
      const intent = await api(`/api/orders/${id}/pay-intent`, { method: 'POST', body: {} });
      window._intent = intent;
      openModal(modalFrame('模拟支付渠道', 'wallet', `
        <div class="detail-rows" style="margin-top:16px">
          <div class="detail-row"><span class="k">订单金额</span><span class="v">¥${formatYuan(intent.amountFen)}</span></div>
          <div class="detail-row"><span class="k">渠道交易号</span><span class="v mono">${esc(intent.channelTxnId)}</span></div>
          <div class="detail-row"><span class="k">通知签名</span><span class="v mono">${esc(intent.signature.slice(0, 16))}…</span></div>
        </div>
        <div class="form-note" style="margin-top:12px">渠道已受理付款并生成<b>带签名的支付通知</b>。投递后服务端会验签、匹配订单与金额，并按渠道交易号做幂等。</div>
        <button class="primary wide" style="margin-top:0" onclick="deliverPay(false)">确认支付（投递渠道通知）</button>
        <button class="secondary wide ghost" onclick="deliverPay(true)">重复投递同一通知（幂等演示）</button>`));
    } catch (e) { toast('支付受理失败：' + e.message); }
  })();
}
async function deliverPay(repeat) {
  const intent = window._intent;
  if (!intent) return;
  if (busy) { toast('操作进行中，请勿重复点击'); return; }
  busy = true;
  try {
    const res = await api('/api/payments/notify', { method: 'POST', body: intent });
    toast(`${repeat ? '重复投递' : '支付'}结果：${res.outcome}｜${res.detail}`, 3600);
    if (!repeat) closeModal();
    await refresh(); render();
  } catch (e) {
    toast('通知被拒绝：' + e.message, 3200);
    await refresh(); render();
  } finally { busy = false; }
}
function cancelOrder(id) {
  confirmAction('取消订单', '取消后订单进入<b>已关闭</b>并释放占用的库存；重复取消不会重复释放。', '确认取消', `runCancel('${esc(id)}')`);
}
function runCancel(id) {
  act(() => api(`/api/orders/${id}/cancel`, { method: 'POST', body: {} }), '订单已关闭，占用库存已释放');
}
function expireOrder(id) {
  confirmAction('模拟超时关闭', '由<b>服务端时间</b>判定为超过 10 分钟未付款，订单关闭并释放占用一次。', '确认超时关闭', `runExpire('${esc(id)}')`);
}
function runExpire(id) {
  act(() => api(`/api/orders/${id}/expire`, { method: 'POST', body: {} }), '服务端判定已超时，订单关闭并释放占用');
}
function applyRefund(id) {
  openModal(modalFrame('申请整单退款', 'refresh', `
    <div class="form-note" style="margin-top:16px">仅「待发货」订单可申请。提交后订单进入<b>退款审核中</b>，仓库发货会被立即挡住；客服拒绝后可继续发货。</div>
    <input id="refundReason" style="display:block;width:100%;padding:10px 12px;border:1px solid #dce4dc;border-radius:8px;outline:0;font:inherit;font-size:13px" placeholder="填写退款原因（选填）"/>
    <button class="primary wide" onclick="submitApplyRefund('${esc(id)}')">提交申请</button>`));
}
function submitApplyRefund(id) {
  const reason = ($('#refundReason') && $('#refundReason').value) || '';
  act(() => api(`/api/orders/${id}/refund-apply`, { method: 'POST', body: { reason } }), '退款申请已提交，审核期间已暂停发货');
}
function latePay(id) {
  act(async () => {
    const intent = await api(`/api/orders/${id}/pay-intent`, { method: 'POST', body: {} });
    const res = await api('/api/payments/notify', { method: 'POST', body: intent });
    toast(`迟到支付：${res.outcome}｜不复活订单、不重占库存`, 3600);
  });
}

/* ================= C 端：我的 ================= */
function renderProfile() {
  const orders = db.orders;
  return `
  <section class="mobile-content">
    <div class="profile-card">
      <div class="avatar">${esc((current.name || '买')[0])}</div>
      <div><h2>${esc(current.name)}</h2><p>买家账号 · ${esc(current.id)}</p></div>
      <span class="verified">${icon('shield', 16)} 已实名</span>
    </div>
    <div class="profile-stats">
      <div><b>${orders.length}</b><span>全部订单</span></div>
      <div><b>${orders.filter((o) => o.status === 'PENDING_PAY').length}</b><span>待付款</span></div>
      <div><b>${orders.filter((o) => o.status === 'PENDING_SHIP').length}</b><span>待发货</span></div>
    </div>
    <div class="permission-card">
      <div class="permission-title">${icon('lock')}<div><h3>你的权限边界</h3><p>系统会在每次请求时校验身份与订单归属</p></div></div>
      <ul>
        <li>${icon('check', 16)} 浏览在售商品并创建订单</li>
        <li>${icon('check', 16)} 支付、取消自己的待付款订单</li>
        <li>${icon('check', 16)} 为自己的未发货订单申请退款</li>
        <li class="disabled">${icon('close', 16)} 查看或操作其他买家的订单</li>
        <li class="disabled">${icon('close', 16)} 商品管理、审核退款或登记发货</li>
      </ul>
      ${renderGuardDemo(['商品上下架', '登记发货', '审核退款', '处理异常支付'])}
    </div>
  </section>`;
}
function renderGuardDemo(actions) {
  const btns = actions.map((a) => `<button onclick="tryForbidden('${a}')">尝试「${a}」</button>`).join('');
  return `<div class="guard-demo"><h4>🔒 越权演示（当前：${esc(ROLES[current.role].label)}）</h4>
    <p>点击按钮将以当前身份<b>真实调用</b>其他角色的后端接口，请求会被权限守卫拦截并返回 403，系统不做任何数据改动。</p>
    <div class="forbidden">${btns}</div></div>`;
}
const FORBIDDEN_CALLS = {
  '商品上下架': () => api('/api/products/P1/onsale', { method: 'POST', body: { onSale: false }, skipGuard: true }),
  '登记发货': () => api('/api/orders/NO20260001/ship', { method: 'POST', body: {}, skipGuard: true }),
  '审核退款': () => api('/api/refunds/RF-DEMO/review', { method: 'POST', body: { approve: true, note: '越权演示' }, skipGuard: true }),
  '处理异常支付': () => api('/api/abnormal/ABN-DEMO/handle', { method: 'POST', body: {}, skipGuard: true }),
  '下单/支付': () => api('/api/orders', { method: 'POST', body: { productId: 'P1', qty: 1 }, skipGuard: true }),
  '新建商品': () => api('/api/products', { method: 'POST', body: { name: '越权商品', priceFen: 100, stock: 1 }, skipGuard: true }),
};
async function tryForbidden(action) {
  let status = 0, msg = '';
  try { await FORBIDDEN_CALLS[action](); msg = '请求未被拦截（异常情况，请检查权限守卫）'; }
  catch (e) { status = e.status; msg = e.message; }
  openModal(`<div class="detail-modal"><div class="deny">
    <div class="ic">⛔</div><div class="t1">无权访问（越权拦截）</div>
    <div class="t2">当前身份：<b>${esc(ROLES[current.role].label)} / ${esc(current.name)}</b><br/>
    操作「${esc(action)}」已被后端拒绝（HTTP ${status || '-'}）。<br/>${esc(msg)}<br/><b>系统未做任何数据改动。</b></div>
    <button class="primary wide" onclick="closeModal()">我知道了</button></div></div>`);
}

/* ================= B 端：工作台 ================= */
function bizMenus(role) {
  if (role === 'ops') return [
    { label: '概览', icon: 'grid' }, { label: '商品管理', icon: 'bag' },
    { label: '订单核查', icon: 'order' }, { label: '资金流水', icon: 'wallet' },
  ];
  if (role === 'warehouse') return [{ label: '发货管理', icon: 'truck' }, { label: '发货记录', icon: 'order' }];
  return [{ label: '退款审核', icon: 'refresh' }, { label: '异常支付', icon: 'alert' }, { label: '处理记录', icon: 'order' }];
}
function bizBadge(label) {
  if (label === '退款审核') return db.refunds.filter((r) => r.status === 'PENDING').length;
  if (label === '异常支付') return db.abnormal.filter((a) => a.status === 'PENDING').length;
  return 0;
}
function renderBusiness() {
  const role = current.role;
  const metaR = ROLE_META[role] || ROLE_META.ops;
  const menus = bizMenus(role);
  const paid = (db.summary && db.summary.receivedFen) || 0;
  const refunded = (db.summary && db.summary.refundedFen) || 0;
  const forbidden = {
    buyer: [], ops: ['下单/支付', '登记发货', '审核退款'],
    warehouse: ['下单/支付', '商品上下架', '审核退款', '处理异常支付'],
    cs: ['下单/支付', '商品上下架', '登记发货'],
  }[role] || [];
  let body = '';
  if (role === 'ops' && bizPage === '概览') body = renderOpsOverview();
  else if (role === 'ops' && bizPage === '商品管理') body = renderProductManagement();
  else if (role === 'ops' && bizPage === '订单核查') body = renderOrderAudit();
  else if (role === 'ops' && bizPage === '资金流水') body = renderLedgerPage(db.ledger);
  else if (role === 'warehouse' && bizPage === '发货管理') body = renderWarehouse();
  else if (role === 'warehouse' && bizPage === '发货记录') body = renderShipments();
  else if (role === 'cs' && bizPage === '退款审核') body = renderRefundReview();
  else if (role === 'cs' && bizPage === '异常支付') body = renderExceptionReview();
  else if (role === 'cs' && bizPage === '处理记录') body = renderLedgerPage(db.ledger.filter((l) => l.type === '退款' || l.type === '异常支付'));
  else body = '<div class="empty-state">页面不存在</div>';
  const roleLabelOf = (r) => r === 'ops' ? '运营专员' : r === 'warehouse' ? '仓库管理员' : '售后客服';
  return `
  <main class="business-page">
    <aside class="sidebar">
      <div class="workspace-title"><span>交易工作台</span><small>ORDER FLOW SYSTEM</small></div>
      <nav>
        ${menus.map((m) => `
        <button class="${bizPage === m.label ? 'active' : ''}" onclick="bizPage='${m.label}';render()">
          ${icon(m.icon, 19)}${m.label}
          ${bizBadge(m.label) ? `<em>${bizBadge(m.label)}</em>` : ''}
        </button>`).join('')}
      </nav>
      <div class="permission-scope">
        <div>${icon('shield', 17)} 当前权限</div>
        ${(PERMISSIONS[role] || []).map((p) => `<span>${icon('check', 13)}${esc(p)}</span>`).join('')}
        ${forbidden.length ? `<div class="guard-demo"><h4>🔒 越权演示</h4><p>真实调用其他角色接口，后端返回 403 并拒绝一切改动。</p><div class="forbidden">${forbidden.map((a) => `<button onclick="tryForbidden('${a}')">${a}</button>`).join('')}</div></div>` : ''}
      </div>
      <button class="logout" onclick="doLogout()">${icon('logout', 18)}退出工作台</button>
    </aside>
    <section class="workspace">
      <header class="workspace-header">
        <div><span class="eyebrow">WORKSPACE</span><h1>${esc(bizPage)}</h1></div>
        <div class="header-tools">
          <button class="icon-button" onclick="refresh().then(()=>{render();toast('数据已刷新')})">${icon('bell')}</button>
          <div class="role-selector">
            <button onclick="showRoleMenu=!showRoleMenu;render()">
              <span class="role-avatar ${role === 'cs' ? 'service' : role}">${icon(metaR.icon, 18)}</span>
              <span><b>${esc(current.name)}</b><small>${esc(roleLabelOf(role))}</small></span>
              <svg class="chev" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">${'<path d="m9 18 6-6-6-6"/>'}</svg>
            </button>
            ${showRoleMenu ? `<div class="role-menu">
              ${['ops', 'warehouse', 'cs'].map((r) => `
              <button class="${role === r ? 'active' : ''}" onclick="switchRole('${r}')">
                <span>${roleLabelOf(r)} · ${(usersOf(r)[0] || {}).name || '-'}</span>
                ${role === r ? icon('check', 15) : ''}
              </button>`).join('')}
            </div>` : ''}
          </div>
        </div>
      </header>
      <div class="workspace-body">${body}</div>
    </section>
  </main>`;
}
async function switchRole(role) {
  if (!current || current.role === role) { showRoleMenu = false; render(); return; }
  const u = usersOf(role)[0];
  if (!u) { toast('该角色没有可用账号'); return; }
  try {
    await loginAs(role, u.id, '');
    showRoleMenu = false;
  } catch (e) { toast('切换身份失败：' + e.message, 3200); showRoleMenu = false; render(); }
}

/* ---------- 运营：概览 ---------- */
function dayKey(t) { const d = new Date(t); const p = (n) => String(n).padStart(2, '0'); return `${p(d.getMonth() + 1)}/${p(d.getDate())}`; }
function renderOpsOverview() {
  const s = db.summary || {};
  const paid = s.receivedFen || 0;
  const refunded = s.refundedFen || 0;
  const pendingAbn = db.abnormal.filter((a) => a.status === 'PENDING').length;
  // 近 7 日收款 / 退款（真实流水聚合）
  const days = [];
  for (let i = 6; i >= 0; i--) {
    const d = new Date(Date.now() - i * 864e5);
    days.push(d);
  }
  const sameDay = (t, d) => { const x = new Date(t); return x.getFullYear() === d.getFullYear() && x.getMonth() === d.getMonth() && x.getDate() === d.getDate(); };
  const income = days.map((d) => db.payments.filter((p) => sameDay(p.createdAt, d)).reduce((sum, p) => sum + p.amountFen, 0));
  const refund = days.map((d) => db.refunds.filter((r) => r.status === 'SUCCEEDED' && sameDay(r.updatedAt, d)).reduce((sum, r) => sum + r.amountFen, 0));
  const maxV = Math.max(1, ...income, ...refund);
  const bars = days.map((d, i) => `
    <div>
      <span style="height:${Math.max(4, Math.round(income[i] / maxV * 100))}%"></span>
      <i style="height:${Math.max(2, Math.round(refund[i] / maxV * 100))}%"></i>
      <small>${i === 6 ? '今天' : dayKey(d)}</small>
    </div>`).join('');
  const yMax = Math.ceil(maxV / 10000) * 10000 || 10000;
  const chartY = [4, 3, 2, 1, 0].map((i) => `<span>¥${formatYuan(yMax * i / 4)}</span>`).join('');
  const lowStock = db.products.filter((p) => p.available <= 2).length;
  return `
  <div class="metric-grid">
    <div class="metric-card"><span class="metric-icon green">${icon('wallet')}</span><div><p>今日收款</p><h2>¥${formatYuan(paid)}</h2><small>已校验渠道通知</small></div></div>
    <div class="metric-card"><span class="metric-icon blue">${icon('order')}</span><div><p>订单总数</p><h2>${s.orderCount || db.orders.length}</h2><small>${db.orders.filter((o) => o.status === 'PENDING_PAY').length} 笔等待付款</small></div></div>
    <div class="metric-card"><span class="metric-icon orange">${icon('refresh')}</span><div><p>退款总额</p><h2>¥${formatYuan(refunded)}</h2><small>以渠道成功为准</small></div></div>
    <div class="metric-card"><span class="metric-icon red">${icon('alert')}</span><div><p>待处理异常</p><h2>${pendingAbn}</h2><small>需客服人工核实</small></div></div>
  </div>
  <div class="dashboard-grid">
    <section class="panel sales-panel">
      <div class="panel-heading"><div><h2>交易趋势</h2><p>近 7 日收款与退款走势</p></div><button class="select-button">近 7 日 ${icon('chevron', 14)}</button></div>
      <div class="chart-area"><div class="chart-y">${chartY}</div><div class="bars">${bars}</div></div>
      <div class="chart-legend"><span><i class="income"></i>收款</span><span><i class="refund"></i>退款</span></div>
    </section>
    <section class="panel">
      <div class="panel-heading"><div><h2>库存健康度</h2><p>商品可售库存实时状态</p></div></div>
      <div class="stock-list">
        ${db.products.map((p) => `
        <div class="${p.available <= 2 ? 'low' : ''}">
          <img src="${esc(p.image)}" alt=""/>
          <span><b>${esc(p.name)}</b><small>${esc(p.id)} · 已售 ${p.sales}</small></span>
          <div class="stock-value"><b>${p.available}</b><small>可售</small></div>
          <div class="stock-bar"><i style="width:${Math.min(100, p.total ? Math.round(p.available / p.total * 100) : 0)}%"></i></div>
        </div>`).join('')}
      </div>
      <div class="stock-note">${icon('alert', 17)} 低于 2 件时触发库存预警${lowStock ? `（当前 ${lowStock} 个商品已预警）` : ''}</div>
    </section>
  </div>
  <section class="panel">
    <div class="panel-heading"><div><h2>最近订单</h2><p>跨支付、履约与退款环节实时核查</p></div>
      <button class="text-button" onclick="bizPage='订单核查';render()">查看全部 ${icon('arrow', 15)}</button></div>
    ${orderTable(db.orders.slice(0, 4), true)}
  </section>`;
}

/* ---------- 运营：商品管理 ---------- */
function renderProductManagement() {
  const q = productQuery.trim().toLowerCase();
  const list = db.products.filter((p) => !q || p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q));
  return `
  <section class="panel full-panel">
    <div class="table-toolbar">
      <div><h2>商品管理</h2><p>维护上下架状态、新建商品与查看可售库存</p></div>
      <div class="toolbar-actions">
        <div class="small-search">${icon('search', 16)}
          <input value="${esc(productQuery)}" placeholder="搜索商品名称 / 编号" aria-label="搜索商品"
            oninput="productQuery=this.value;renderProductTable()"/>
          ${productQuery ? `<button class="search-clear" onclick="productQuery='';render()" aria-label="清空搜索">${icon('close', 14)}</button>` : ''}
        </div>
        <button class="primary" onclick="openCreateProduct()">新建商品</button>
      </div>
    </div>
    <div id="productTableWrap">${productTable(list)}</div>
  </section>`;
}
function productTable(list) {
  return `
    <div class="data-table">
      <div class="tr th"><span>商品信息</span><span>价格</span><span>总库存</span><span>可售库存</span><span>状态</span><span>操作</span></div>
      ${list.length === 0 ? '<div class="empty-state">没有匹配的商品</div>' : ''}
      ${list.map((p) => `
      <div class="tr">
        <span class="product-cell"><img src="${esc(p.image)}" alt=""/><i><b>${esc(p.name)}</b><small>${esc(p.id)} · 已售 ${p.sales}</small></i></span>
        <span>¥${formatYuan(p.priceFen)}</span>
        <span>${p.total}</span>
        <span><b>${p.available}</b> 件</span>
        <span><i class="dot ${p.onSale ? 'online' : ''}"></i>${p.onSale ? '在售' : '已下架'}</span>
        <span><button class="${p.onSale ? 'danger-link' : 'link-button'}" onclick="toggleSale('${esc(p.id)}',${!p.onSale})">${p.onSale ? '下架' : '上架'}</button></span>
      </div>`).join('')}
    </div>`;
}
function renderProductTable() {
  const q = productQuery.trim().toLowerCase();
  const list = db.products.filter((p) => !q || p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q));
  const wrap = $('#productTableWrap');
  if (wrap) wrap.innerHTML = productTable(list);
}
function openCreateProduct() {
  openModal(modalFrame('新建商品', 'bag', `
    <p class="form-note" style="margin-top:16px">创建后默认在售，可在列表中下架。金额以「元」输入、按「分」提交。</p>
    <div class="form-grid">
      <label>商品名称<input id="npName" placeholder="例如：便携保温杯"/></label>
      <label>价格（元）<input id="npPrice" type="number" min="0.01" step="0.01" value="100"/></label>
      <label>库存（件）<input id="npStock" type="number" min="0" step="1" value="5"/></label>
    </div>
    <button class="primary wide" onclick="submitCreateProduct()">确认创建</button>`));
}
function submitCreateProduct() {
  const name = ($('#npName') && $('#npName').value.trim()) || '';
  const price = parseFloat(($('#npPrice') && $('#npPrice').value) || '0');
  const stock = parseInt(($('#npStock') && $('#npStock').value) || '-1', 10);
  if (!name) { toast('请填写商品名称'); return; }
  if (!Number.isFinite(price) || price <= 0) { toast('请填写有效价格'); return; }
  if (!Number.isInteger(stock) || stock < 0) { toast('库存必须为非负整数'); return; }
  const priceFen = Math.round(price * 100);
  act(() => api('/api/products', { method: 'POST', body: { name, priceFen, stock } }), '商品已创建并上架');
}
function toggleSale(pid, onSale) {
  act(() => api(`/api/products/${pid}/onsale`, { method: 'POST', body: { onSale } }), onSale ? '商品已重新上架' : '商品已下架，买家端将不可购买');
}

/* ---------- 运营：订单核查 ---------- */
function orderTable(list, embedded) {
  const options = ['全部', ...STATUS_ORDER.map((k) => stOf(k).label)];
  const visible = auditFilter === '全部' ? list : list.filter((o) => stOf(o.status).label === auditFilter);
  return `
  <div class="data-table order-table ${embedded ? 'embedded' : 'panel full-panel'}">
    ${!embedded ? `
    <div class="table-toolbar">
      <div><h2>全量订单核查</h2><p>运营仅可查看，不可直接修改订单状态</p></div>
      <button class="secondary" onclick="filterOpen=!filterOpen;render()">${icon('filter', 16)} 筛选${auditFilter !== '全部' ? ` · ${auditFilter}` : ''}</button>
    </div>
    ${filterOpen ? `
    <div class="filter-panel">
      <span>订单状态</span>
      <div class="filter-chips">${options.map((s) => `<button class="${auditFilter === s ? 'active' : ''}" onclick="auditFilter='${s}';render()">${s}</button>`).join('')}</div>
      <small>共 ${visible.length} 笔订单</small>
    </div>` : ''}` : ''}
    <div class="tr th"><span>订单 / 商品</span><span>买家</span><span>金额</span><span>订单状态</span><span>创建时间</span><span>操作</span></div>
    ${visible.length === 0 ? '<div class="empty-state">没有符合筛选条件的订单</div>' : ''}
    ${visible.map((o) => `
    <div class="tr">
      <span class="product-cell"><img src="${esc(o.productImage)}" alt=""/><i><b>${esc(o.id)}</b><small>${esc(o.productName)}</small></i></span>
      <span>${esc(o.buyerName)}</span>
      <span>¥${formatYuan(o.amountFen)}</span>
      <span>${badge(o.status)}</span>
      <span>${fmtRelTime(o.createdAt)}</span>
      <span><button class="link-button" onclick="openOrderLedger('${esc(o.id)}')">核查流水</button></span>
    </div>`).join('')}
  </div>`;
}
function renderOrderAudit() { return orderTable(db.orders, false); }
function openOrderLedger(orderId) {
  const list = db.ledger.filter((l) => l.orderId === orderId);
  const row = (l) => `<div class="detail-row"><span class="k">${esc(l.type)}</span><span class="v">${l.amountFen ? '¥' + formatYuan(l.amountFen) : '—'} · ${esc(l.note)} <span class="mono">(${fmtTime(l.createdAt)})</span></span></div>`;
  openModal(modalFrame('订单流水核查', 'wallet', `
    <div class="detail-rows" style="margin-top:16px">
      ${list.length ? list.map(row).join('') : '<div class="empty-state">该订单暂无流水</div>'}
    </div>`));
}

/* ---------- 流水页 / 导出 ---------- */
function ledgerTable(list) {
  return `
  <div class="data-table ledger-table">
    <div class="tr th"><span>流水号</span><span>类型</span><span>关联订单</span><span>金额</span><span>记录时间</span><span>处理说明</span></div>
    ${list.length === 0 ? '<div class="empty-state">暂无流水记录</div>' : ''}
    ${list.map((l) => `
    <div class="tr">
      <span class="mono">${esc(l.id)}</span>
      <span><i class="ledger-dot ${esc(l.type)}"></i>${esc(l.type)}</span>
      <span class="mono">${esc(l.orderId || '-')}</span>
      <span>${l.amountFen ? '¥' + formatYuan(l.amountFen) : '—'}</span>
      <span>${fmtTime(l.createdAt)}</span>
      <span>${esc(l.note)}</span>
    </div>`).join('')}
  </div>`;
}
function renderLedgerPage(list) {
  return `
  <section class="panel full-panel">
    <div class="table-toolbar">
      <div><h2>不可变业务流水</h2><p>记录只增不改，异常不可通过改状态消失（共 ${list.length} 条）</p></div>
      <button class="secondary" onclick="exportLedgerCsv()">导出记录</button>
    </div>
    ${ledgerTable(list.slice().reverse())}
  </section>`;
}
function exportLedgerCsv() {
  const list = db.ledger || [];
  const rows = [['流水号', '类型', '关联订单', '金额(分)', '记录时间', '说明']];
  list.forEach((l) => rows.push([l.id, l.type, l.orderId || '', l.amountFen, fmtTime(l.createdAt), l.note]));
  const csv = '\uFEFF' + rows.map((r) => r.map((c) => `"${String(c).replace(/"/g, '""')}"`).join(',')).join('\r\n');
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = 'ledger.csv';
  a.click();
  URL.revokeObjectURL(a.href);
  toast('流水已导出为 CSV');
}

/* ---------- 仓库 ---------- */
function renderWarehouse() {
  const shippable = db.orders.filter((o) => o.status === 'PENDING_SHIP');
  return `
  <div class="info-banner">${icon('shield')}<div><b>发货状态校验已启用</b><p>仅「待发货」订单可登记一次发货；退款审核中的订单已自动从列表移除，重复登记会被后端拒绝。</p></div></div>
  <section class="panel full-panel">
    <div class="table-toolbar">
      <div><h2>待发货任务</h2><p>共 ${shippable.length} 笔合法待处理订单</p></div>
      <div class="small-search">${icon('search', 16)}<span>搜索订单号</span></div>
    </div>
    <div class="fulfillment-grid">
      ${shippable.length === 0 ? '<div class="empty-state">暂无待发货订单</div>' : ''}
      ${shippable.map((o) => `
      <article>
        <div class="fulfill-head"><span><i></i>待发货</span><small>${fmtRelTime(o.createdAt)}</small></div>
        <div class="fulfill-product">
          <img src="${esc(o.productImage)}" alt=""/>
          <div><b>${esc(o.productName)}</b><span>${esc(o.id)}</span><small>收件人：${esc(o.buyerName)}</small></div>
          <strong>× ${o.qty}</strong>
        </div>
        <div class="fulfill-footer">
          <span>实付 <b>¥${formatYuan(o.amountFen)}</b></span>
          <button class="primary" onclick="shipOrder('${esc(o.id)}')">${icon('truck', 17)}登记发货</button>
        </div>
      </article>`).join('')}
    </div>
  </section>`;
}
function shipOrder(id) {
  act(() => api(`/api/orders/${id}/ship`, { method: 'POST', body: { carrier: '顺丰速运', trackingNo: '' } }),
    '发货登记成功，该订单不可重复发货');
}
function renderShipments() {
  return `
  <section class="panel full-panel">
    <div class="table-toolbar"><div><h2>已登记发货</h2><p>发货记录对订单唯一（共 ${db.shipments.length} 条）</p></div></div>
    <div class="data-table ledger-table">
      <div class="tr th"><span>发货单号</span><span>关联订单</span><span>承运方</span><span>运单号</span><span>发货时间</span><span>操作人</span></div>
      ${db.shipments.length === 0 ? '<div class="empty-state">暂无发货记录</div>' : ''}
      ${db.shipments.map((sh) => `
      <div class="tr">
        <span class="mono">${esc(sh.id)}</span>
        <span class="mono">${esc(sh.orderId)}</span>
        <span>${esc(sh.carrier)}</span>
        <span class="mono">${esc(sh.trackingNo)}</span>
        <span>${fmtTime(sh.createdAt)}</span>
        <span>${esc(sh.operator || '-')}</span>
      </div>`).join('')}
    </div>
  </section>`;
}

/* ---------- 客服：退款审核 ---------- */
function orderFor(orderId) { return db.orders.find((o) => o.id === orderId) || {}; }
function renderRefundReview() {
  return `
  <div class="info-banner warm">${icon('alert')}<div><b>审核后将触发明确的状态迁移</b><p>同意并经渠道退款成功后恢复库存；拒绝后订单回到待发货状态。售前退款恢复库存，迟到支付退款不恢复。</p></div></div>
  <div class="review-list">
    ${db.refunds.length === 0 ? '<div class="empty-state">暂无退款单</div>' : ''}
    ${db.refunds.map((r) => {
      const o = orderFor(r.orderId);
      const rst = REFUND_STATUS[r.status] || { label: r.status };
      let actions = '';
      if (r.status === 'PENDING') {
        actions = `
          <button class="secondary danger" onclick="rejectRefund('${esc(r.id)}')">拒绝申请</button>
          <button class="primary" onclick="reviewRefund('${esc(r.id)}')">同意并退款</button>`;
      } else if (r.status === 'AWAITING') {
        actions = `
          <button class="secondary danger" onclick="channelRefund('${esc(r.id)}',false)">渠道返回失败</button>
          <button class="primary" onclick="channelRefund('${esc(r.id)}',true)">渠道确认成功</button>`;
      } else if (r.status === 'FAILED') {
        actions = `<button class="primary" onclick="retryRefund('${esc(r.id)}')">重试退款</button>`;
      } else {
        actions = `<span class="status status-${esc(rst.label)}">${esc(rst.label)}</span>`;
      }
      return `
      <article class="review-card">
        <div class="review-main">
          <div class="review-tag ${r.source === 'late' ? 'red' : ''}">${r.source === 'late' ? '迟到支付退款' : '售前退款'}</div>
          <h3>${esc(r.id)}</h3>
          <p>申请时间：${fmtRelTime(r.createdAt)} · 买家：${esc(o.buyerName || '-')} · 第 ${r.attempts} 次发起</p>
          <div class="review-product">
            <img src="${esc(o.productImage || 'images/product1.png')}" alt=""/>
            <span><b>${esc(o.productName || r.orderId)}</b><small>${esc(rst.label)} · 整单退款</small></span>
            <strong>¥${formatYuan(r.amountFen)}</strong>
          </div>
        </div>
        <div class="review-reason">
          <span>买家原因</span>
          <p>${esc(r.reason || '-')}</p>
          <small>${r.reviewNote ? '审核备注：' + esc(r.reviewNote) + ' · ' : ''}退款金额 <b>¥${formatYuan(r.amountFen)}</b></small>
        </div>
        <div class="review-actions">${actions}</div>
      </article>`;
    }).join('')}
  </div>`;
}
function reviewRefund(rid) {
  act(() => api(`/api/refunds/${rid}/review`, { method: 'POST', body: { approve: true, note: '核实无误，同意退款' } }),
    '已同意，等待渠道确认退款结果');
}
function rejectRefund(rid) {
  openModal(modalFrame('拒绝退款申请', 'close', `
    <div class="form-note" style="margin-top:16px">拒绝后订单退回「待发货」，仓库可继续发货；请写明拒绝原因。</div>
    <input id="reviewNote" style="display:block;width:100%;padding:10px 12px;border:1px solid #dce4dc;border-radius:8px;outline:0;font:inherit;font-size:13px" placeholder="填写拒绝原因"/>
    <button class="primary wide" onclick="submitReject('${esc(rid)}')">提交拒绝</button>`));
}
function submitReject(rid) {
  const note = ($('#reviewNote') && $('#reviewNote').value) || '';
  if (!note.trim()) { toast('请填写拒绝原因'); return; }
  act(() => api(`/api/refunds/${rid}/review`, { method: 'POST', body: { approve: false, note } }), '退款已拒绝，订单恢复待发货');
}
function channelRefund(rid, success) {
  act(() => api(`/api/refunds/${rid}/channel-notify`, { method: 'POST', body: { success } }),
    success ? '渠道确认退款成功' : '渠道退款失败，退款单保留待处理可重试');
}
function retryRefund(rid) {
  act(() => api(`/api/refunds/${rid}/retry`, { method: 'POST', body: {} }), '已重新向渠道发起退款');
}

/* ---------- 客服：异常支付 ---------- */
function renderExceptionReview() {
  return `
  <div class="info-banner danger-banner">${icon('alert')}<div><b>迟到支付不会复活已关闭订单</b><p>人工核实后原路退款，不重新占用或恢复已在关单时释放的库存。</p></div></div>
  <div class="review-list">
    ${db.abnormal.length === 0 ? '<div class="empty-state">暂无异常支付</div>' : ''}
    ${db.abnormal.map((a) => {
      const o = orderFor(a.orderId);
      return `
      <article class="review-card">
        <div class="review-main">
          <div class="review-tag ${a.status === 'PENDING' ? 'red' : ''}">${ABNORMAL_STATUS[a.status] || a.status}</div>
          <h3>${esc(a.id)}</h3>
          <p>发现时间：${fmtRelTime(a.createdAt)} · 买家：${esc(o.buyerName || '-')}</p>
          <div class="review-product">
            <img src="${esc(o.productImage || 'images/product1.png')}" alt=""/>
            <span><b>${esc(o.productName || a.orderId)}</b><small>${esc(a.reason)}</small></span>
            <strong>¥${formatYuan(a.amountFen)}</strong>
          </div>
        </div>
        <div class="exception-flow"><span>订单关闭</span><i></i><span>迟到支付</span><i></i><b>${a.status === 'PENDING' ? '待人工退款' : '已原路退回'}</b></div>
        <div class="review-actions">
          <button class="secondary" onclick="openPayProof('${esc(a.id)}')">查看支付凭证</button>
          ${a.status === 'PENDING' ? `<button class="primary" onclick="handleAbnormal('${esc(a.id)}')">核实并原路退款</button>` : `<span class="status status-已退款">已退款</span>`}
        </div>
      </article>`;
    }).join('')}
  </div>`;
}
function openPayProof(aid) {
  const a = db.abnormal.find((x) => x.id === aid);
  if (!a) return;
  const o = orderFor(a.orderId);
  const row = (k, v) => `<div class="detail-row"><span class="k">${k}</span><span class="v">${v}</span></div>`;
  openModal(modalFrame('支付凭证', 'wallet', `
    <div class="detail-rows" style="margin-top:16px">
      ${row('订单号', `<span class="mono">${esc(a.orderId)}</span>`)}
      ${row('商品', esc(o.productName || '-'))}
      ${row('金额', '¥' + formatYuan(a.amountFen))}
      ${row('渠道交易号', `<span class="mono">${esc(a.channelTxnId || '-')}</span>`)}
      ${row('异常原因', esc(a.reason))}
      ${row('订单状态', badge(o.status || 'CLOSED'))}
    </div>`));
}
function handleAbnormal(aid) {
  act(() => api(`/api/abnormal/${aid}/handle`, { method: 'POST', body: {} }),
    '异常款项已发起原路退款，库存保持不变');
}

/* ================= 倒计时 ================= */
setInterval(() => {
  document.querySelectorAll('[data-cd]').forEach((el) => {
    const left = Number(el.dataset.cd) - Date.now();
    el.textContent = fmtClock(left);
  });
  // 有订单到期且尚未提示刷新时，自动刷新一次（服务端超时扫描会关单）
  if (current && current.role === 'buyer' && !cdReloaded) {
    const anyDue = db.orders.some((o) => o.status === 'PENDING_PAY' && new Date(o.expiresAt).getTime() <= Date.now());
    if (anyDue) {
      cdReloaded = true;
      refresh().then(() => { if (SIDE === 'c' && buyerTab !== 'profile') render(); });
    }
  }
}, 1000);

/* ================= 启动 ================= */
(async function init() {
  try { meta = await api('/api/meta'); } catch (e) { meta = null; }
  renderLogin();
})();

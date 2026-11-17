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
const SIDE_ROLES = SIDE === 'c' ? ['buyer'] : ['ops', 'warehouse', 'cs', 'admin'];
const ROLES = {
  buyer: { label: '买家', name: 'C 端 · 买家', ico: '🛒', desc: '逛店、下单、支付、取消、申请退款（仅自己的订单）' },
  ops: { label: '运营', name: 'B 端 · 工作台', ico: '📊', desc: '商品管理、订单核查、资金流水（不能改订单状态）' },
  warehouse: { label: '仓库', name: 'B 端 · 工作台', ico: '📦', desc: '查待发货、登记发货、给商品补货' },
  cs: { label: '客服', name: 'B 端 · 工作台', ico: '🎧', desc: '审核退款、渠道确认、处理迟到支付异常' },
  admin: { label: '管理员', name: 'B 端 · 工作台', ico: '🛡', desc: '查看并操作全部 B 端功能，维护 B 端员工账号，看不到 C 端数据' },
};
const ROLE_META = {
  ops: { name: '林舟', label: '运营专员', icon: 'chart' },
  warehouse: { name: '周师傅', label: '仓库管理员', icon: 'box' },
  cs: { name: '陈禾', label: '售后客服', icon: 'headset' },
  admin: { name: '系统管理员', label: '管理员', icon: 'shield' },
  buyer: { name: '张小买', label: '买家', icon: 'user' },
};
const STATUS = {
  PENDING_PAY: { label: '待付款' },
  PENDING_SHIP: { label: '待发货' },
  REFUNDING: { label: '退款审核中' },
  SHIPPED: { label: '已发货' },
  SIGNED: { label: '已签收' },
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
const STATUS_ORDER = ['PENDING_PAY', 'PENDING_SHIP', 'REFUNDING', 'SHIPPED', 'SIGNED', 'CLOSED', 'REFUNDED', 'ABNORMAL_PAY'];

const PERMISSIONS = {
  ops: ['商品上下架', '库存查看', '交易汇总', '全链路对账'],
  warehouse: ['待发货查看', '登记发货', '商品补货'],
  cs: ['退款审核', '异常支付处理'],
  admin: ['查看全部 B 端数据', 'B 端全功能操作', '新增 B 端员工账号'],
  buyer: ['浏览商品', '下单支付', '取消订单', '申请退款'],
};

let current = null;      // { role, id, name, token }
let meta = null;
let buyerTab = 'shop';
let bizPage = '概览';
let orderFilter = 'all';
let auditFilter = '全部';
let filterOpen = false;
let shopQuery = '';
let productQuery = '';
let busy = false;
let cdReloaded = false;
let db = {
  products: [], orders: [], refunds: [], abnormal: [], ledger: [], shipments: [],
  payments: [], notifyResults: [], summary: null,
  productOps: [],   // 商品操作记录（新增 / 上架 / 下架），与资金流水分开
  threads: [],      // 客服会话列表
  addresses: [],    // 买家收货地址（仅买家加载）
  staff: [],        // B 端员工账号（仅管理员加载）
};

const $ = (s) => document.querySelector(s);
const formatYuan = (f) => (Number(f || 0) / 100).toFixed(2);
const esc = (v) => String(v == null ? '' : v).replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const stOf = (s) => STATUS[s] || { label: s || '-' };

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
function closeModal() {
  const hadChat = !!chatOrder;
  document.querySelectorAll('.modal-backdrop').forEach((m) => m.remove());
  // 会话弹窗关闭后必须停掉 4 秒一次的轮询
  if (typeof stopChatPoll === 'function') stopChatPoll();
  // 聊完回到页面时刷新一次：客服侧未读徽标、买家侧「客服有新回复」提示都要立刻同步
  if (hadChat && current) {
    refresh().then(() => {
      if (document.querySelector('.modal-backdrop')) return;
      if (current.role === 'cs' && bizPage !== '客服消息') return;
      render();
    }).catch(() => {});
  }
}
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
  { re: /^\/api\/addresses(\/[^/]+(\/default)?)?$/, roles: ['buyer'], action: '收货地址管理' },
  { re: /^\/api\/orders\/[^/]+\/sign$/, roles: ['buyer'], action: '确认收货' },
  { re: /^\/api\/products\/[^/]+\/reviews$/, roles: ['buyer'], action: '商品评价' },
  { re: /^\/api\/orders\/[^/]+\/ship$/, roles: ['warehouse'], action: '登记发货' },
  { re: /^\/api\/products$/, roles: ['ops'], action: '商品管理' },
  { re: /^\/api\/products\/[^/]+\/onsale$/, roles: ['ops', 'admin'], action: '商品上下架' },
  { re: /^\/api\/products\/[^/]+\/stock$/, roles: ['warehouse', 'ops', 'admin'], action: '商品补货' },
  { re: /^\/api\/staff$/, roles: ['admin'], action: 'B 端员工管理' },
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
  // 文件上传统一走 FormData：此时不能手写 Content-Type，交给浏览器带 boundary
  const isForm = typeof FormData !== 'undefined' && o.body instanceof FormData;
  const headers = { 'X-Client': SIDE };
  if (!isForm) headers['Content-Type'] = 'application/json';
  if (current) {
    headers['X-Role'] = current.role; headers['X-User-Id'] = current.id;
    if (current.token) headers['X-Token'] = current.token;
  }
  const res = await fetch(path, {
    method: o.method || 'GET', headers,
    body: o.body === undefined ? undefined : (isForm ? o.body : JSON.stringify(o.body)),
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
    // 买家也拉会话摘要：用于订单卡上的「客服已回复」提示与轮询弹提醒
    const [products, orders, threads, addrs] = await Promise.all([
      api('/api/products'), api('/api/orders'), api('/api/chat/threads'), api('/api/addresses'),
    ]);
    db.products = products || []; db.orders = orders || []; db.threads = threads || [];
    db.addresses = addrs || [];
  } else if (current.role === 'ops' || current.role === 'admin') {
    // 管理员与运营共用同一份全量快照；管理员额外拉员工账号
    const reqs = [api('/api/state'), api('/api/summary')];
    if (current.role === 'admin') reqs.push(api('/api/staff'), api('/api/chat/threads'));
    const [st, summary, staff, threads] = await Promise.all(reqs);
    db.products = st.products || []; db.orders = st.orders || []; db.ledger = st.ledger || [];
    db.notifyResults = st.notifyResults || []; db.refunds = st.refunds || [];
    db.abnormal = st.abnormalPayments || []; db.shipments = st.shipments || [];
    db.payments = st.payments || []; db.summary = summary;
    db.productOps = st.productOps || [];
    if (current.role === 'admin') { db.staff = staff || []; db.threads = threads || []; }
  } else if (current.role === 'warehouse') {
    const [orders, shipments, products] = await Promise.all([
      api('/api/orders'), api('/api/shipments'), api('/api/products'),
    ]);
    db.orders = orders || []; db.shipments = shipments || []; db.products = products || [];
  } else if (current.role === 'cs') {
    const [refunds, abnormal, orders, threads] = await Promise.all([
      api('/api/refunds'), api('/api/abnormal'), api('/api/orders'), api('/api/chat/threads'),
    ]);
    db.refunds = refunds || []; db.abnormal = abnormal || []; db.orders = orders || [];
    db.threads = threads || [];
  }
}

/* ================= 实时刷新（跨端状态同步） ================= */
// 仓库登记发货、买家签收、客服退款后，另一端不需要重新登录即可看到最新状态：
// 每 8 秒静默轮询一次，数据有变化才重绘；弹窗打开、正在输入、操作进行中时跳过，避免打断操作。
let pollTimer = null;
let lastSnap = '';
function dbSnapshot() {
  return JSON.stringify({
    o: db.orders, p: db.products, r: db.refunds, a: db.abnormal,
    s: db.shipments, l: db.ledger, t: db.threads, po: db.productOps,
  });
}
function pollingPaused() {
  if (!current || busy) return true;
  if (document.querySelector('.modal-backdrop')) return true;
  const ae = document.activeElement;
  return !!(ae && /^(INPUT|TEXTAREA|SELECT)$/.test(ae.tagName));
}
async function pollOnce() {
  if (pollingPaused()) return;
  try {
    await refresh();
    if (current.role === 'buyer') syncBuyerChatNotice(false);
    const snap = dbSnapshot();
    if (snap !== lastSnap) { lastSnap = snap; render(); }
  } catch (e) { /* 轮询失败不打断用户，下一轮再试 */ }
}
function startPolling() {
  stopPolling();
  pollTimer = setInterval(pollOnce, 8000);
}
function stopPolling() { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } }
// 切回标签页 / 窗口获得焦点时立即刷新一次，比等 8 秒更快
document.addEventListener('visibilitychange', () => { if (!document.hidden) pollOnce(); });
window.addEventListener('focus', () => { pollOnce(); });

/* ================= 登录 ================= */
// 登录页只提供「账号 + 密码」两个输入框：角色由服务端账号表判定，
// 前端既不能选角色，也拿不到任何账号清单。
function renderLogin() {
  const hint = SIDE === 'c'
    ? `当前是 <b>C 端买家页面</b>（独立端口），仅接受买家账号。运营 / 仓库 / 客服请打开 <b>B 端工作台</b> 页面登录。`
    : `当前是 <b>B 端工作台</b>（独立端口），仅接受运营 / 仓库 / 客服 / 管理员账号。买家请打开 <b>C 端买家页面</b> 登录。`;
  $('#app').innerHTML = `
  <div class="login-page"><div class="login-card">
    <div class="login-brand">
      <span class="brand-mark">${icon('logo', 24)}</span>
      <div><h1>衡序交易</h1><small>小型电商订单交易系统 · ${SIDE_LABEL}</small></div>
    </div>
    <div class="login-hint">${hint}</div>
    <form class="login-form" onsubmit="doLogin();return false">
      <div class="login-field"><label for="accountInput">手机号</label>
        <input id="accountInput" type="tel" inputmode="numeric" maxlength="11" autocomplete="username"
          autocapitalize="off" spellcheck="false" placeholder="请输入 11 位手机号" autofocus/></div>
      <div class="login-field"><label for="pwdInput">登录密码</label>
        <input id="pwdInput" type="password" autocomplete="current-password" placeholder="请输入登录密码"/></div>
      <button class="enter-btn" type="submit" id="loginBtn">登 录</button>
    </form>
    ${SIDE === 'c' ? `<div class="login-register">还没有账号？<button type="button" onclick="openRegister()">注册新账号</button></div>` : ''}
    <div class="login-subtle">手机号为登录账号，账号与密码由服务端账号表校验，登录后只能进入该角色对应的页面。金额单位统一为「分」。</div>
  </div></div>`;
}
// 注册：手机号 + 密码 + 昵称，注册成功自动回填手机号，用户直接登录即可
function openRegister() {
  openModal(modalFrame('注册买家账号', 'user', `
    <p class="form-note" style="margin-top:16px">手机号即登录账号（11 位数字），密码 6–32 位；注册后可直接用手机号登录 C 端。B 端账号不开放自助注册，由管理员在工作台「员工管理」里新增。</p>
    <div class="form-grid">
      <label>手机号<input id="regPhone" type="tel" inputmode="numeric" maxlength="11" placeholder="请输入 11 位手机号"/></label>
      <label>昵称<input id="regName" placeholder="不填自动生成" maxlength="20"/></label>
      <label>密码<input id="regPwd" type="password" placeholder="6–32 位"/></label>
      <label>确认密码<input id="regPwd2" type="password" placeholder="再输入一次"/></label>
    </div>
    <div class="form-note" id="regNote" style="margin-bottom:14px"></div>
    <button class="primary wide" id="regBtn" onclick="submitRegister()">注册并返回登录</button>`));
}
async function submitRegister() {
  const phone = (($('#regPhone') && $('#regPhone').value) || '').trim();
  const name = (($('#regName') && $('#regName').value) || '').trim();
  const pwd = ($('#regPwd') && $('#regPwd').value) || '';
  const pwd2 = ($('#regPwd2') && $('#regPwd2').value) || '';
  const note = $('#regNote');
  const fail = (msg) => { if (note) { note.textContent = '⚠ ' + msg; note.style.color = '#b4554e'; } toast(msg); };
  if (!/^\d{11}$/.test(phone)) { fail('手机号必须是 11 位数字'); return; }
  if (pwd.length < 6) { fail('密码至少 6 位'); return; }
  if (pwd !== pwd2) { fail('两次输入的密码不一致'); return; }
  const btn = $('#regBtn');
  if (btn) { btn.disabled = true; btn.textContent = '注册中…'; }
  try {
    await api('/api/register', { method: 'POST', body: { phone, password: pwd, name }, skipGuard: true });
    closeModal();
    toast('注册成功，请用手机号登录');
    const acc = $('#accountInput');
    if (acc) { acc.value = phone; acc.focus(); }
    const p = $('#pwdInput');
    if (p) p.focus();
  } catch (e) {
    if (btn) { btn.disabled = false; btn.textContent = '注册并返回登录'; }
    fail('注册失败：' + e.message);
  }
}
// 登录成功后 current 的唯一来源是服务端响应（role / id / name 均不可前端伪造）。
async function loginAs(phone, password) {
  const sess = await api('/api/session', { method: 'POST', body: { phone, password } });
  const u = (sess && sess.user) || {};
  if (!u.role || !u.id) throw new Error('登录响应异常，请重试');
  if (!SIDE_ROLES.includes(u.role)) {
    throw new Error(`${ROLES[u.role] ? ROLES[u.role].label : u.role} 账号不属于 ${SIDE_LABEL}`);
  }
  current = { role: u.role, id: u.id, name: u.name || u.id, phone: u.phone || phone, token: (sess && sess.token) || '' };
  db = {
    products: [], orders: [], refunds: [], abnormal: [], ledger: [], shipments: [],
    payments: [], notifyResults: [], summary: null, productOps: [], threads: [], addresses: [], staff: [],
  };
  buyerTab = 'shop';
  // 落地页严格按角色决定：运营/管理员→概览、仓库→发货管理、客服→退款审核
  bizPage = (u.role === 'ops' || u.role === 'admin') ? '概览' : u.role === 'warehouse' ? '发货管理' : '退款审核';
  orderFilter = 'all'; auditFilter = '全部'; filterOpen = false; shopQuery = ''; productQuery = ''; cdReloaded = false;
  await refresh();
  lastSnap = dbSnapshot();
  chatToastSeen = {};
  syncBuyerChatNotice(true); // 登录时静默登记未读，只有之后的新回复才弹提醒
  render();
  startPolling(); // 登录后开启跨端实时同步
  toast(`已登录：${ROLES[u.role] ? ROLES[u.role].label : u.role} · ${current.name}`);
}
function doLogin() {
  const id = (($('#accountInput') && $('#accountInput').value) || '').trim();
  const pwd = ($('#pwdInput') && $('#pwdInput').value) || '';
  if (!id) { toast('请输入手机号'); $('#accountInput').focus(); return; }
  // 与服务端一致：登录账号必须是 11 位纯数字手机号
  if (!/^\d{11}$/.test(id)) { toast('手机号必须是 11 位数字'); $('#accountInput').focus(); return; }
  if (!pwd) { toast('请输入登录密码'); $('#pwdInput').focus(); return; }
  const btn = $('#loginBtn');
  if (btn) { btn.disabled = true; btn.textContent = '登录中…'; }
  (async () => {
    try {
      await loginAs(id, pwd);
    } catch (e) {
      toast('登录失败：' + e.message, 3200);
      if (btn) { btn.disabled = false; btn.textContent = '登 录'; }
      const p = $('#pwdInput');
      if (p) { p.value = ''; p.focus(); }
    }
  })();
}
function doLogout() {
  stopHeroTimer(); stopPolling(); stopChatPoll();
  current = null; lastSnap = ''; closeModal(); renderLogin();
}

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
  $('#app').innerHTML = `
  <div class="app-shell">
    <header class="top-switcher">
      <div class="brand"><span class="brand-mark">${icon('logo', 22)}</span><span>衡序交易</span></div>
    </header>
    ${SIDE === 'c' ? renderCustomer() : renderBusiness()}
  </div>`;
  // 只在本端逛逛页启动轮播定时器，切走/退出时立刻停掉，避免后台空转
  startHeroTimer();
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
// 首页轮播：4 张主图，4.5 秒自动切换，可点圆点或左右箭头手动切换。
const HERO_SLIDES = [
  { tag: '限时好物计划', title: '认真挑选，<br/>简单抵达。', desc: '每件商品都经过精选，安心交易有迹可循。', image: 'images/product1.png', note: '限量热卖' },
  { tag: '新品家电专场', title: '把日常，<br/>交给好电器。', desc: '电饭煲 / 空气炸锅，厨房效率翻倍。', image: 'images/appliance.svg', note: '新品上架' },
  { tag: '童趣时光', title: '陪孩子，<br/>玩出想象力。', desc: '益智积木与遥控玩具，材质安心。', image: 'images/toy.svg', note: '亲子精选' },
  { tag: '静心阅读', title: '一本书，<br/>一段好时光。', desc: '儿童绘本与经典名著，读得进去的好书。', image: 'images/book.svg', note: '编辑推荐' },
];
const SHOP_CATEGORIES = ['家用电器', '电子产品', '儿童玩具', '书本', '食品', '家居生活', '其他'];
let heroIdx = 0;
let heroTimer = null;
let shopCategory = '全部';

function heroSlideHTML() {
  const s = HERO_SLIDES[heroIdx % HERO_SLIDES.length];
  return `<div class="hero-copy">
      <span>${s.tag}</span>
      <h1>${s.title}</h1>
      <p>${s.desc}</p>
      <button onclick="document.getElementById('products')?.scrollIntoView({behavior:'smooth'})">立即选购 ${icon('arrow', 17)}</button>
    </div>
    <div class="hero-visual"><img src="${esc(s.image)}" alt="${s.tag}"/><span class="floating-tag">${s.note}</span></div>`;
}
function heroDotsHTML() {
  return HERO_SLIDES.map((s, i) => `<button class="hero-dot${i === heroIdx % HERO_SLIDES.length ? ' active' : ''}"
    onclick="gotoHero(${i})" aria-label="第 ${i + 1} 张：${s.tag}"></button>`).join('');
}
function renderHero() {
  const s = $('#heroSlides');
  if (s) s.innerHTML = heroSlideHTML();
  const d = $('#heroDots');
  if (d) d.innerHTML = heroDotsHTML();
}
function gotoHero(i) {
  heroIdx = (i + HERO_SLIDES.length) % HERO_SLIDES.length;
  renderHero();
  startHeroTimer(); // 手动切换后重新计时
}
function heroStep(d) { gotoHero(heroIdx + d); }
function startHeroTimer() {
  stopHeroTimer();
  if (!current || SIDE !== 'c' || buyerTab !== 'shop' || !$('#heroSlides')) return;
  heroTimer = setInterval(() => {
    if (!$('#heroSlides')) { stopHeroTimer(); return; }
    heroIdx = (heroIdx + 1) % HERO_SLIDES.length;
    renderHero();
  }, 4500);
}
function stopHeroTimer() {
  if (heroTimer) { clearInterval(heroTimer); heroTimer = null; }
}
function filteredProducts() {
  const q = shopQuery.trim().toLowerCase();
  return db.products.filter((p) => {
    if (shopCategory !== '全部' && p.category !== shopCategory) return false;
    if (!q) return true;
    return p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q) || (p.category || '').toLowerCase().includes(q);
  });
}
function renderShop() {
  const list = filteredProducts();
  return `
    <div class="search-box">${icon('search', 19)}
      <input id="shopSearch" value="${esc(shopQuery)}" placeholder="搜索好物" aria-label="搜索商品"
        oninput="shopQuery=this.value;renderShopGrid()"/>
      <kbd>搜索</kbd>
    </div>
    <section class="customer-hero carousel">
      <div class="hero-slides" id="heroSlides">${heroSlideHTML()}</div>
      <button class="hero-arrow prev" onclick="heroStep(-1)" aria-label="上一张">${icon('chevron', 18)}</button>
      <button class="hero-arrow next" onclick="heroStep(1)" aria-label="下一张">${icon('chevron', 18)}</button>
      <div class="hero-dots" id="heroDots">${heroDotsHTML()}</div>
    </section>
    <section class="trust-strip">
      <div>${icon('shield')}<span><b>支付保障</b><small>渠道校验，重复不扣款</small></span></div>
      <div>${icon('refresh')}<span><b>售前可退</b><small>未发货支持整单退款</small></span></div>
      <div>${icon('truck')}<span><b>履约可查</b><small>发货节点全程留痕</small></span></div>
    </section>
    <section class="products-section" id="products">
      <div class="section-heading">
        <div><span class="eyebrow">CURATED FOR YOU</span><h2>本周精选</h2></div>
        <button onclick="pickCategory('全部')">查看全部 ${icon('chevron', 15)}</button>
      </div>
      <div class="category-row" id="categoryRow">
        ${['全部', ...SHOP_CATEGORIES].map((c) => `<button class="chip${c === shopCategory ? ' active' : ''}"
          onclick="pickCategory('${c}')">${c}</button>`).join('')}
      </div>
      <div class="product-grid" id="shopGrid">${shopCards(list)}</div>
    </section>`;
}
function shopCards(list) {
  if (!list.length) return '<div class="empty-state">该分类下暂无匹配的商品</div>';
  return list.map((p) => `
    <article class="product-card" key="${esc(p.id)}">
      <button class="product-image" onclick="openProductSheet('${esc(p.id)}')">
        <img src="${esc(p.image)}" alt="${esc(p.name)}"/>
        <span>${!p.onSale ? '已下架' : (p.available > 0 ? `仅剩 ${p.available} 件` : '已售罄')}</span>
      </button>
      <div class="product-info">
        <p class="product-code">${esc(p.id)} · ${esc(p.category || '其他')}</p>
        <h3 onclick="openProductSheet('${esc(p.id)}')">${esc(p.name)}</h3>
        <p>已售 ${p.sales} 件</p>
        <p class="old-price">¥${formatYuan(p.oldPriceFen)}</p>
        <div class="product-bottom">
          <strong>¥${formatYuan(p.priceFen)}</strong>
          <button onclick="openBuySheet('${esc(p.id)}')" ${p.onSale && p.available > 0 ? '' : 'disabled'}>${icon('bag', 18)}</button>
        </div>
      </div>
    </article>`).join('');
}
function pickCategory(c) {
  shopCategory = c;
  document.querySelectorAll('#categoryRow .chip').forEach((b) => b.classList.toggle('active', b.textContent === c));
  renderShopGrid();
}
function renderShopGrid() {
  const g = $('#shopGrid');
  if (g) g.innerHTML = shopCards(filteredProducts());
}
/* ---------- 商品详情：图片 / 价格 / 评论区（无人评论时提示「暂无人评论」） ---------- */
function openProductSheet(pid) {
  const p = db.products.find((x) => x.id === pid);
  if (!p) return;
  const canBuy = p.onSale && p.available > 0;
  openModal(`<div class="buy-sheet product-detail-sheet">
    <button class="modal-close" onclick="closeModal()" aria-label="关闭">${icon('close')}</button>
    <div class="sheet-product">
      <img src="${esc(p.image)}" alt=""/>
      <div><p>${esc(p.name)}</p><strong>¥${formatYuan(p.priceFen)}</strong>
        <span>${esc(p.category || '其他')} · 已售 ${p.sales} 件 · ${canBuy ? `可售 ${p.available} 件` : productStatus(p)}</span></div>
    </div>
    <div class="detail-review-head"><b>商品评价</b><span id="pdReviewCount"></span></div>
    <div class="pd-reviews" id="pdReviews"><div class="empty-state">加载中…</div></div>
    <div class="sheet-total"><span>单价</span><strong>¥${formatYuan(p.priceFen)}</strong></div>
    <button class="primary wide" onclick="${canBuy ? `openBuySheet('${esc(p.id)}')` : 'null'}" ${canBuy ? '' : 'disabled'}>${canBuy ? '选择购买' : productStatus(p)}</button>
  </div>`);
  loadProductReviews(pid);
}
async function loadProductReviews(pid) {
  const box = $('#pdReviews');
  if (!box) return;
  let list = [];
  try { list = (await api(`/api/products/${pid}/reviews`)) || []; } catch (e) { list = []; }
  const tops = list.filter((r) => !r.parentId).slice().reverse();
  const count = $('#pdReviewCount');
  if (count) count.textContent = tops.length ? `${tops.length} 条` : '';
  if (!tops.length) {
    box.innerHTML = '<div class="empty-state pd-empty">暂无人评论</div>';
    return;
  }
  box.innerHTML = tops.map((r) => `
    <article class="pd-review">
      <div class="pd-review-head"><b>${esc(r.buyerName)}</b><small>${fmtRelTime(r.createdAt)}</small></div>
      <p>${esc(r.content)}</p>
      ${list.filter((c) => c.parentId === r.id).map((c) => `
        <div class="pd-review-reply"><b>${esc(c.buyerName)}</b><span>${esc(c.content)}</span></div>`).join('')}
    </article>`).join('');
}
const CH_LABEL = { wechat: '微信支付', alipay: '支付宝支付' };
function defaultAddress() {
  const list = db.addresses || [];
  return list.find((a) => a.isDefault) || list[0] || null;
}
function openBuySheet(pid) {
  const p = db.products.find((x) => x.id === pid);
  if (!p) return;
  if (!p.onSale) { toast('商品已下架'); return; }
  window._buyPid = pid;
  window._buyQty = window._buyQty || 1;
  window._buyChannel = window._buyChannel || 'wechat';
  if (!window._buyAddr || !(db.addresses || []).some((a) => a.id === window._buyAddr)) {
    const d = defaultAddress();
    window._buyAddr = d ? d.id : '';
  }
  const addrs = db.addresses || [];
  const sel = addrs.find((a) => a.id === window._buyAddr) || null;
  openModal(`<div class="buy-sheet">
    <button class="modal-close" onclick="closeModal()" aria-label="关闭">${icon('close')}</button>
    <div class="sheet-product">
      <img src="${esc(p.image)}" alt=""/>
      <div><p>${esc(p.name)}</p><strong>¥${formatYuan(p.priceFen)}</strong><span>库存 ${p.available} 件 · 每单限购 2 件</span></div>
    </div>
    <button type="button" class="addr-bar" onclick="openAddrPicker(true)">
      ${sel ? `<span class="addr-bar-pin">${icon('order', 15)}</span>
        <div class="addr-bar-main"><b>${esc(sel.name)} ${esc(sel.phone)}</b>${sel.isDefault ? '<em>默认</em>' : ''}
          <p>${esc(sel.region)} ${esc(sel.detail)}</p></div>`
      : `<span class="addr-bar-pin warn">${icon('order', 15)}</span>
        <div class="addr-bar-main"><b class="warn">请选择收货地址</b><p>下单前需要填写收货地址</p></div>`}
      <span class="addr-bar-arrow">›</span>
    </button>
    <div class="sheet-sec"><b>支付方式</b></div>
    <div class="pay-options">
      <button type="button" class="pay-opt ${window._buyChannel === 'wechat' ? 'sel' : ''}" onclick="pickPay('wechat')"><i class="pay-dot wechat"></i>微信支付</button>
      <button type="button" class="pay-opt ${window._buyChannel === 'alipay' ? 'sel' : ''}" onclick="pickPay('alipay')"><i class="pay-dot alipay"></i>支付宝支付</button>
    </div>
    <div class="quantity-row"><span>购买数量</span>
      <div class="stepper">
        <button onclick="chgQty(-1)" aria-label="减少">−</button>
        <b id="qtyN">${window._buyQty}</b>
        <button onclick="chgQty(1)" aria-label="增加">＋</button>
      </div>
    </div>
    <div class="rule-box">${icon('shield', 18)}<div><b>交易保障</b><p>下单即占库存，10 分钟内未支付将自动关闭并释放库存。</p></div></div>
    <div class="sheet-total"><span>应付</span><strong id="qtySum">¥${formatYuan(p.priceFen * window._buyQty)}</strong></div>
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
// 重绘下单面板（保留已选数量 / 地址 / 支付方式），选择项高亮由 openBuySheet 依据 window 状态输出
function rerenderBuySheet() { if (window._buyPid) openBuySheet(window._buyPid); }
function pickPay(ch) { window._buyChannel = ch; rerenderBuySheet(); }

/* ---------- 收货地址页：列表（选择 / 设默认 / 复制 / 修改 / 删除） ---------- */
function openAddrPicker(fromBuy) {
  window._addrFromBuy = !!fromBuy;
  renderAddrPicker();
}
function addrBack() {
  if (window._addrFromBuy) rerenderBuySheet(); else { closeModal(); render(); }
}
function renderAddrPicker() {
  const list = db.addresses || [];
  const selId = window._buyAddr;
  const fromBuy = !!window._addrFromBuy;
  openModal(`<div class="addr-page">
    <div class="addr-page-head">
      <button class="addr-back" onclick="addrBack()" aria-label="返回">‹</button>
      <b>${fromBuy ? '选择收货地址' : '收货地址'}</b>
      <span class="addr-page-count">${list.length}/10</span>
    </div>
    <div class="addr-page-body">
      ${list.length ? list.map((a) => `
        <div class="addr-page-item ${a.id === selId ? 'sel' : ''}">
          <div class="addr-page-top">
            <div class="addr-page-main">
              <p class="addr-page-line"><b>${esc(a.name)}，${esc(a.phone)}</b></p>
              <p class="addr-page-text">${esc(a.region)} ${esc(a.detail)}</p>
            </div>
            ${fromBuy
              ? (a.id === selId ? '<span class="addr-used">✓</span>' : `<button class="addr-use" onclick="useAddr('${esc(a.id)}')">使用</button>`)
              : ''}
          </div>
          <div class="addr-page-ops">
            <button class="addr-def ${a.isDefault ? 'on' : ''}" onclick="setDefaultAddr('${esc(a.id)}')">
              <i>${a.isDefault ? '✓' : ''}</i>${a.isDefault ? '已设默认' : '默认'}</button>
            <button class="text-button" onclick="copyAddr('${esc(a.id)}')">复制</button>
            <button class="text-button" onclick="openAddressEditor('${esc(a.id)}')">修改</button>
            <button class="text-button danger" onclick="confirmDelAddr('${esc(a.id)}')">${window._delPending === a.id ? '确认删除' : '删除'}</button>
          </div>
        </div>`).join('')
      : '<div class="addr-page-empty">还没有收货地址<br/><span>点击下方按钮添加，最多保存 10 个</span></div>'}
    </div>
    <div class="addr-page-foot">
      <button class="primary wide" onclick="openAddressEditor('')">＋ 新增收货地址</button>
    </div>
  </div>`);
}
function useAddr(id) {
  window._buyAddr = id;
  rerenderBuySheet();
  toast('已选择该收货地址');
}
function copyAddr(id) {
  const a = (db.addresses || []).find((x) => x.id === id);
  if (!a) return;
  const text = `${a.name} ${a.phone} ${a.region} ${a.detail}`;
  const done = () => toast('地址已复制');
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(done).catch(() => fallbackCopy(text, done));
  } else fallbackCopy(text, done);
}
function fallbackCopy(text, done) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.position = 'fixed';
  ta.style.opacity = '0';
  document.body.appendChild(ta);
  ta.select();
  try { document.execCommand('copy'); done(); } catch (e) { toast('复制失败，请手动选择地址文本'); }
  document.body.removeChild(ta);
}
function submitOrder() {
  const pid = window._buyPid;
  const qty = window._buyQty || 1;
  const addressId = window._buyAddr;
  if (!addressId) { toast('请先选择或新增收货地址'); openAddrPicker(true); return; }
  (async () => {
    const o = await act(() => api('/api/orders', {
      method: 'POST',
      body: { productId: pid, qty, channel: window._buyChannel || 'wechat', addressId },
    }));
    if (o) {
      buyerTab = 'orders';
      render();
      toast(`下单成功（${CH_LABEL[o.channel] || o.channel}），库存已为你保留 10 分钟`);
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
    if (orderFilter === 'done') return ['SHIPPED', 'SIGNED', 'CLOSED', 'REFUNDED'].includes(o.status);
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
  // 客服在该订单会话里有新回复时，卡片上直接提示，点击进入会话查看
  const th = (db.threads || []).find((x) => x.orderId === o.id);
  const csReplied = !!(th && threadUnread(th));
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
    acts.push(`<button class="primary" onclick="signOrder('${esc(o.id)}')">确认收货</button>`);
  } else if (o.status === 'SIGNED') {
    acts.push(`<button class="secondary" onclick="openReviews('${esc(o.productId)}','${esc(o.id)}')">评价 / 查看评论</button>`);
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
    ${o.receiverName ? `<div class="order-recv">${icon('order', 13)} ${esc(o.receiverName)} ${esc(maskPhone(o.receiverPhone))} · ${esc(o.receiverAddress)}<em>${esc(CH_LABEL[o.channel] || o.channel || '-')}</em></div>` : ''}
    <div class="order-actions">${acts.join('')}</div>
    <div class="order-service"><button class="text-button ${csReplied ? 'notice' : ''}" onclick="openOrderChat('${esc(o.id)}')">${icon('headset', 15)} ${csReplied ? '客服有新回复 · 点击查看' : '联系客服'}</button></div>
  </article>`;
}
// 确认收货：已发货 → 已签收，签收时间同步回仓库发货记录
function signOrder(id) {
  confirmAction('确认收货', '确认已经收到商品？签收后订单进入「已签收」，不可再申请退款，但可以对商品发表评价。',
    '确认已收到', `doSign('${id}')`);
}
function doSign(id) {
  act(() => api(`/api/orders/${id}/sign`, { method: 'POST', body: {} }), '已确认收货，订单状态：已签收');
}
function openOrderDetail(id) {
  const o = db.orders.find((x) => x.id === id);
  if (!o) { toast('订单不存在'); return; }
  const payDone = o.status !== 'PENDING_PAY';
  const shipDone = ['SHIPPED', 'SIGNED'].includes(o.status) || (!!o.shipmentId && (o.status === 'REFUNDED' || o.status === 'CLOSED'));
  const signedDone = o.status === 'SIGNED';
  const row = (k, v) => `<div class="detail-row"><span class="k">${k}</span><span class="v">${v}</span></div>`;
  openModal(modalFrame('订单详情', 'order', `
    <div class="timeline">
      <div class="done"><i>${icon('check', 14)}</i><div><b>订单已创建</b><span>${fmtRelTime(o.createdAt)} · 商品价格已快照</span></div></div>
      <div class="${payDone ? 'done' : ''}"><i>${payDone ? icon('check', 14) : ''}</i><div><b>支付校验</b><span>${payDone ? '金额与订单匹配，收款成功' : '等待付款'}</span></div></div>
      <div class="${shipDone ? 'done' : ''}"><i>${shipDone ? icon('check', 14) : ''}</i><div><b>仓库履约</b><span>${shipDone ? '已登记发货' : o.status === 'REFUNDING' ? '退款审核中，发货已冻结' : '等待合法状态后处理'}</span></div></div>
      <div class="${signedDone ? 'done' : ''}"><i>${signedDone ? icon('check', 14) : ''}</i><div><b>买家签收</b><span>${signedDone ? '买家已确认收货，可评价商品' : '等待买家确认收货'}</span></div></div>
    </div>
    <div class="detail-rows">
      ${row('状态', badge(o.status))}
      ${row('商品', esc(o.productName))}
      ${row('单价快照', '¥' + formatYuan(o.priceFen))}
      ${row('数量 / 合计', o.qty + ' 件 / ¥' + formatYuan(o.amountFen))}
      ${row('支付方式', esc(CH_LABEL[o.channel] || o.channel || '-'))}
      ${o.receiverName ? row('收货人', `${esc(o.receiverName)} · ${esc(maskPhone(o.receiverPhone))}`) : ''}
      ${o.receiverAddress ? row('收货地址（快照）', esc(o.receiverAddress)) : ''}
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
      const oo = orderFor(id);
      openModal(modalFrame('模拟支付渠道', 'wallet', `
        <div class="detail-rows" style="margin-top:16px">
          <div class="detail-row"><span class="k">订单金额</span><span class="v">¥${formatYuan(intent.amountFen)}</span></div>
          <div class="detail-row"><span class="k">支付方式</span><span class="v">${esc((oo && CH_LABEL[oo.channel]) || '-')}</span></div>
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
  const avatar = loadAvatar();
  return `
  <section class="mobile-content">
    <div class="profile-card">
      <button class="avatar avatar-editable" type="button" onclick="pickAvatar()" title="点击更换头像">
        ${avatar ? `<img src="${esc(avatar)}" alt="头像"/>` : esc((current.name || '买')[0])}
        <span class="avatar-edit">${icon('user', 12)}</span>
      </button>
      <div><h2>${esc(current.name)}</h2><p>买家账号 · ${esc(current.phone || current.id)}</p></div>
      <span class="verified">${icon('shield', 16)} 已实名</span>
    </div>
    <div class="profile-stats">
      <div><b>${orders.length}</b><span>全部订单</span></div>
      <div><b>${orders.filter((o) => o.status === 'PENDING_PAY').length}</b><span>待付款</span></div>
      <div><b>${orders.filter((o) => o.status === 'PENDING_SHIP').length}</b><span>待发货</span></div>
    </div>
    <div class="addr-card">
      ${(() => {
        const d = defaultAddress(); const n = (db.addresses || []).length;
        return `<button class="addr-card-entry" type="button" onclick="openAddrPicker(false)">
          <span class="addr-bar-pin">${icon('order', 15)}</span>
          <div class="addr-bar-main">
            ${d ? `<b>${esc(d.name)} ${esc(d.phone)}</b>${d.isDefault ? '<em>默认</em>' : ''}<p>${esc(d.region)} ${esc(d.detail)}</p>`
                : '<b class="warn">还没有收货地址</b><p>添加后下单可直接选用</p>'}
            ${n > 1 ? `<span class="addr-card-more">共 ${n} 个地址</span>` : ''}
          </div>
          <span class="addr-bar-arrow">›</span>
        </button>`;
      })()}
    </div>
    <button class="logout-wide" onclick="confirmLogout()">${icon('logout', 18)} 退出登录</button>
  </section>`;
}
/* ---------- 收货地址管理（我的页） ---------- */
function maskPhone(p) { return p && p.length === 11 ? p.slice(0, 3) + '****' + p.slice(7) : (p || ''); }
// 新增 / 修改地址表单（保存后回到地址列表页，保持「选择地址 → 下单」的连贯流程）
function openAddressEditor(id) {
  const a = id ? (db.addresses || []).find((x) => x.id === id) : null;
  window._editAddrId = a ? a.id : '';
  openModal(`<div class="addr-page">
    <div class="addr-page-head">
      <button class="addr-back" onclick="renderAddrPicker()" aria-label="返回">‹</button>
      <b>${a ? '修改收货地址' : '新增收货地址'}</b>
      <span class="addr-page-count"></span>
    </div>
    <div class="addr-page-body">
      <div class="addr-form-page">
        <label>收货人<input id="naName" maxlength="20" placeholder="收货人姓名" value="${a ? esc(a.name) : ''}"/></label>
        <label>手机号<input id="naPhone" maxlength="11" inputmode="numeric" placeholder="11 位手机号" value="${a ? esc(a.phone) : ''}"/></label>
        <label>所在地区<input id="naRegion" maxlength="60" placeholder="省 / 市 / 区" value="${a ? esc(a.region) : ''}"/></label>
        <label>详细地址<input id="naDetail" maxlength="100" placeholder="街道、门牌号" value="${a ? esc(a.detail) : ''}"/></label>
        <label class="addr-default"><input type="checkbox" id="naDefault" ${!a || a.isDefault ? 'checked' : ''}/> 设为默认地址</label>
        <p class="addr-form-tip">最多保存 10 个地址；订单会记录下单时的地址快照，之后修改不影响历史订单。</p>
      </div>
    </div>
    <div class="addr-page-foot"><button class="primary wide" onclick="saveAddress()">保存</button></div>
  </div>`);
}
async function saveAddress() {
  const body = {
    name: (($('#naName') && $('#naName').value) || '').trim(),
    phone: (($('#naPhone') && $('#naPhone').value) || '').trim(),
    region: (($('#naRegion') && $('#naRegion').value) || '').trim(),
    detail: (($('#naDetail') && $('#naDetail').value) || '').trim(),
    isDefault: !!(($('#naDefault') || {}).checked),
  };
  if (!body.name || !body.region || !body.detail) { toast('请完整填写收货人、所在地区与详细地址'); return; }
  if (!/^\d{11}$/.test(body.phone)) { toast('手机号必须是 11 位数字'); return; }
  const id = window._editAddrId;
  if (busy) return;
  busy = true;
  try {
    const saved = id
      ? await api(`/api/addresses/${id}`, { method: 'PUT', body })
      : await api('/api/addresses', { method: 'POST', body });
    await refresh();
    if (!window._buyAddr && saved) window._buyAddr = saved.id;
    toast(id ? '地址已更新' : '地址已保存');
    window._delPending = '';
    renderAddrPicker();
  } catch (e) {
    toast('保存失败：' + e.message, 3200);
  } finally { busy = false; }
}
async function setDefaultAddr(id) {
  if (busy) return;
  busy = true;
  try {
    await api(`/api/addresses/${id}/default`, { method: 'POST', body: {} });
    await refresh();
    toast('已设为默认地址');
    renderAddrPicker();
  } catch (e) { toast('操作被拒绝：' + e.message, 3200); } finally { busy = false; }
}
// 删除做二次确认：同一按钮再点一次才真正删除，避免误触
function confirmDelAddr(id) {
  if (window._delPending === id) { runDelAddr(id); return; }
  window._delPending = id;
  renderAddrPicker();
  toast('再次点击「确认删除」即可删除该地址', 3200);
  setTimeout(() => {
    if (window._delPending !== id) return;
    window._delPending = '';
    if (document.querySelector('.addr-page')) renderAddrPicker();
  }, 4000);
}
async function runDelAddr(id) {
  if (busy) return;
  busy = true;
  try {
    await api(`/api/addresses/${id}`, { method: 'DELETE' });
    await refresh();
    if (window._buyAddr === id) { const d = defaultAddress(); window._buyAddr = d ? d.id : ''; }
    window._delPending = '';
    toast('地址已删除');
    renderAddrPicker();
    render();
  } catch (e) { toast('删除失败：' + e.message, 3200); } finally { busy = false; }
}
/* ---------- 用户头像：从本机选图，压缩后按账号存在浏览器本地 ---------- */
function avatarKey() { return 'sco:avatar:' + (current ? current.id : ''); }
function loadAvatar() {
  try { return localStorage.getItem(avatarKey()) || ''; } catch (e) { return ''; }
}
function pickAvatar() {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = 'image/png,image/jpeg,image/webp,image/gif';
  input.onchange = () => {
    const file = input.files && input.files[0];
    if (!file) return;
    if (file.size > 5 * 1024 * 1024) { toast('图片不要超过 5MB'); return; }
    const reader = new FileReader();
    reader.onload = () => {
      const img = new Image();
      img.onload = () => {
        // 居中裁成正方形再缩到 160px，避免大头像把 localStorage 撑爆
        const side = Math.min(img.width, img.height);
        const canvas = document.createElement('canvas');
        canvas.width = canvas.height = 160;
        const ctx = canvas.getContext('2d');
        ctx.drawImage(img, (img.width - side) / 2, (img.height - side) / 2, side, side, 0, 0, 160, 160);
        const dataUrl = canvas.toDataURL('image/jpeg', 0.86);
        try { localStorage.setItem(avatarKey(), dataUrl); } catch (e) { toast('头像保存失败'); return; }
        toast('头像已更新');
        render();
      };
      img.onerror = () => toast('图片解析失败，请换一张');
      img.src = reader.result;
    };
    reader.readAsDataURL(file);
  };
  input.click();
}
// 退出登录：二次确认，避免误触；退出后清空本地身份并回到手机号登录页。
function confirmLogout() {
  openModal(`<div class="detail-modal"><div class="deny">
    <div class="ic">👋</div><div class="t1">退出登录</div>
    <div class="t2">当前账号：<b>${esc(current.phone || current.id)} · ${esc(current.name)}</b><br/>
      退出后需要重新输入手机号与密码才能进入。</div>
    <button class="primary wide" onclick="doLogout()">确认退出</button>
    <button class="ghost wide" onclick="closeModal()">取消</button></div></div>`);
}

/* ================= B 端：工作台 ================= */
function bizMenus(role) {
  if (role === 'ops') return [
    { label: '概览', icon: 'grid' }, { label: '商品管理', icon: 'bag' },
    { label: '商品记录', icon: 'bag' }, // 新增 / 上下架留痕，与资金流水分开
    { label: '订单核查', icon: 'order' }, { label: '资金流水', icon: 'wallet' },
  ];
  if (role === 'warehouse') return [
    { label: '发货管理', icon: 'truck' }, { label: '库存管理', icon: 'box' },
    { label: '发货记录', icon: 'order' }, { label: '签收记录', icon: 'check' },
  ];
  if (role === 'admin') return [
    { label: '概览', icon: 'grid' }, { label: '商品管理', icon: 'bag' }, { label: '商品记录', icon: 'bag' },
    { label: '订单核查', icon: 'order' }, { label: '资金流水', icon: 'wallet' },
    { label: '发货管理', icon: 'truck' }, { label: '库存管理', icon: 'box' },
    { label: '发货记录', icon: 'order' }, { label: '签收记录', icon: 'check' },
    { label: '客服消息', icon: 'headset' }, { label: '退款审核', icon: 'refresh' },
    { label: '异常支付', icon: 'alert' }, { label: '处理记录', icon: 'order' },
    { label: '员工管理', icon: 'user' },
  ];
  return [
    { label: '客服消息', icon: 'headset' },
    { label: '退款审核', icon: 'refresh' }, { label: '异常支付', icon: 'alert' }, { label: '处理记录', icon: 'order' },
  ];
}
function bizBadge(label) {
  if (label === '退款审核') return db.refunds.filter((r) => ['PENDING', 'AWAITING', 'FAILED'].includes(r.status)).length;
  if (label === '异常支付') return db.abnormal.filter((a) => a.status === 'PENDING').length;
  // 客服消息徽标只提示「买家发了还没回」的会话，不等于会话总数
  if (label === '客服消息') return unreadChatCount();
  return 0;
}
function renderBusiness() {
  const role = current.role;
  const metaR = ROLE_META[role] || ROLE_META.ops;
  const menus = bizMenus(role);
  // 只渲染当前角色菜单里有的页面：手工改 bizPage 也进不去别的角色页面
  if (!menus.some((m) => m.label === bizPage)) bizPage = menus[0].label;
  const pages = {
    '概览': () => renderOpsOverview(),
    '商品管理': () => renderProductManagement(),
    '商品记录': () => renderProductOps(),
    '订单核查': () => renderOrderAudit(),
    '资金流水': () => renderLedgerPage(db.ledger.filter((l) => !/^商品/.test(l.type || ''))),
    '发货管理': () => renderWarehouse(),
    '库存管理': () => renderInventory(),
    '发货记录': () => renderShipments(),
    '签收记录': () => renderSignRecords(),
    '客服消息': () => renderCsChats(),
    '退款审核': () => renderRefundReview(),
    '异常支付': () => renderExceptionReview(),
    '处理记录': () => renderCsRecords(),
    '员工管理': () => renderStaffAdmin(),
  };
  const body = pages[bizPage] ? pages[bizPage]() : '<div class="empty-state">页面不存在</div>';
  // 角色标注用短名，避免与账号姓名（如「仓库管理员」）重复出现同一串文字
  const roleChip = (r) => r === 'ops' ? '运营'
    : r === 'warehouse' ? '仓库'
      : r === 'admin' ? '管理员' : '客服';
  return `
  <main class="business-page">
    <aside class="sidebar">
      <div class="workspace-title"><span>交易工作台</span><small>ORDER FLOW SYSTEM</small></div>
      <nav>
        ${menus.map((m) => `
        <button class="${bizPage === m.label ? 'active' : ''}" onclick="bizPage='${m.label}';render()">
          ${icon(m.icon, 19)}${m.label}
          ${bizBadge(m.label) ? `<em data-badge="${esc(m.label)}">${bizBadge(m.label)}</em>` : `<em data-badge="${esc(m.label)}" hidden></em>`}
        </button>`).join('')}
      </nav>
    </aside>
    <section class="workspace">
      <header class="workspace-header">
        <div><span class="eyebrow">WORKSPACE</span><h1>${esc(bizPage)}</h1></div>
        <div class="header-tools">
          <div class="user-card" title="当前登录账号">
            <span class="role-avatar ${role === 'cs' ? 'service' : role}">${icon(metaR.icon, 18)}</span>
            <span class="user-card-meta">
              <b>${esc(current.name || roleChip(role))}</b>
              <i class="role-tag ${esc(role)}">${esc(roleChip(role))}</i>
            </span>
          </div>
          <button class="logout-btn" onclick="confirmLogout()" title="退出当前账号">
            ${icon('logout', 16)}<span>退出登录</span>
          </button>
        </div>
      </header>
      <div class="workspace-body">${body}</div>
    </section>
  </main>`;
}

/* ---------- 运营：概览 ---------- */
function dayKey(t) { const d = new Date(t); const p = (n) => String(n).padStart(2, '0'); return `${p(d.getMonth() + 1)}/${p(d.getDate())}`; }
// 统计区间：近三天 / 近一周 / 本月 / 上个月 / 今年 / 去年（KPI 与趋势图共用同一口径）
const STAT_RANGES = [
  { key: '3d', label: '近三天' },
  { key: '7d', label: '近一周' },
  { key: 'month', label: '本月' },
  { key: 'lastMonth', label: '上个月' },
  { key: 'year', label: '今年' },
  { key: 'lastYear', label: '去年' },
];
let trendRange = '7d';
function trendRangeLabel() {
  const r = STAT_RANGES.find((x) => x.key === trendRange);
  return r ? r.label : '近一周';
}
// rangeOf 返回区间 [from, to) 与分桶单位（日 / 月）
// 关键点：本月只算到「今天」，不把未来的日期画进图里
function rangeOf(key) {
  const now = new Date();
  const y = now.getFullYear();
  const m = now.getMonth();
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const tomorrow = new Date(today);
  tomorrow.setDate(tomorrow.getDate() + 1);
  if (key === 'month') return { from: new Date(y, m, 1), to: tomorrow, unit: 'day' };
  if (key === 'lastMonth') return { from: new Date(y, m - 1, 1), to: new Date(y, m, 1), unit: 'day' };
  if (key === 'year') return { from: new Date(y, 0, 1), to: tomorrow, unit: 'month' };
  if (key === 'lastYear') return { from: new Date(y - 1, 0, 1), to: new Date(y, 0, 1), unit: 'month' };
  const span = key === '3d' ? 3 : 7;
  const from = new Date(today);
  from.setDate(from.getDate() - (span - 1));
  return { from, to: tomorrow, unit: 'day' };
}
// rangeBuckets 生成柱状图的分桶：日区间按天，年区间按月（12 根不会挤）
function rangeBuckets(key) {
  const { from, to, unit } = rangeOf(key);
  const out = [];
  if (unit === 'month') {
    for (let i = 0; i < 12; i++) {
      out.push({
        start: new Date(from.getFullYear(), i, 1),
        end: new Date(from.getFullYear(), i + 1, 1),
        label: `${i + 1}月`,
      });
    }
    return out;
  }
  for (let d = new Date(from); d < to; d.setDate(d.getDate() + 1)) {
    out.push({
      start: new Date(d),
      end: new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1),
      label: `${d.getMonth() + 1}/${String(d.getDate()).padStart(2, '0')}`,
    });
  }
  return out;
}
function inRange(t, from, to) { const x = new Date(t); return !isNaN(x) && x >= from && x < to; }
// 区间统计口径与后端 Summary 一致：只认资金流水里的「收款 / 退款」，商品动作流水不计入
function rangeStats(key) {
  const { from, to } = rangeOf(key);
  const rows = db.ledger.filter((l) => !/^商品/.test(l.type || '') && inRange(l.createdAt, from, to));
  const income = rows.filter((l) => l.type === '收款').reduce((a, l) => a + l.amountFen, 0);
  const refund = rows.filter((l) => l.type === '退款').reduce((a, l) => a + l.amountFen, 0);
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const tomorrow = new Date(today);
  tomorrow.setDate(tomorrow.getDate() + 1);
  const todayIncome = db.ledger
    .filter((l) => l.type === '收款' && inRange(l.createdAt, today, tomorrow))
    .reduce((a, l) => a + l.amountFen, 0);
  return {
    from, to, income, refund, net: income - refund, todayIncome,
    orders: db.orders.filter((o) => inRange(o.createdAt, from, to)),
  };
}
function pickTrend(range) { trendRange = range; render(); }
function renderOpsOverview() {
  const stat = rangeStats(trendRange);
  const rangeLabel = trendRangeLabel();
  const pendingAbn = db.abnormal.filter((a) => a.status === 'PENDING').length;
  const buckets = rangeBuckets(trendRange);
  // 柱子表达「净收款」（收款 − 退款），与 KPI 卡口径一致：退款不会叠加进柱子
  const bucketRows = buckets.map((b) => {
    const rows = db.ledger.filter((l) => inRange(l.createdAt, b.start, b.end));
    const plus = rows.filter((l) => l.type === '收款').reduce((a, l) => a + l.amountFen, 0);
    const minus = rows.filter((l) => l.type === '退款').reduce((a, l) => a + l.amountFen, 0);
    return { plus, minus, net: Math.max(0, plus - minus) };
  });
  const maxV = Math.max(1, ...bucketRows.map((r) => r.net));
  // 标签抽稀：12 个以内全显示（如 7 天 / 12 个月），更长的日区间按 6 等分取点，首尾必留
  const labelStep = buckets.length <= 12 ? 1 : Math.ceil(buckets.length / 6);
  const lastIdx = buckets.length - 1;
  const bars = buckets.map((b, i) => {
    const r = bucketRows[i];
    // 末尾标签必留；若上一个标签离末尾太近则让位，避免两个日期挤在一起
    const show = labelStep === 1 || i === lastIdx
      || (i % labelStep === 0 && lastIdx - i >= 2);
    return `
    <div class="bar-col" title="${b.label} 净收款 ¥${formatYuan(r.net)}（收款 ¥${formatYuan(r.plus)} − 退款 ¥${formatYuan(r.minus)}）">
      <div class="bar-track"><span class="bar-fill${r.net > 0 ? '' : ' zero'}"${r.net > 0 ? ` style="height:${Math.max(3, Math.round(r.net / maxV * 100))}%"` : ''}></span></div>
      <small class="${show ? '' : 'hide'}">${esc(b.label)}</small>
    </div>`;
  }).join('');
  const yMax = Math.ceil(maxV / 10000) * 10000 || 10000;
  const chartY = [4, 3, 2, 1, 0].map((i) => `<span>¥${formatYuan(yMax * i / 4)}</span>`).join('');
  // 有流水的桶数量：零值天画淡灰基线，避免「图里没东西 = 数据丢了」的误解
  const activeBuckets = bucketRows.filter((r) => r.plus > 0 || r.minus > 0).length;
  const activeText = activeBuckets === 0
    ? '该区间暂无流水'
    : `其中 ${activeBuckets} ${rangeOf(trendRange).unit === 'month' ? '个月' : '天'}有流水`;
  const lowStock = db.products.filter((p) => p.available <= 2).length;
  return `
  <div class="metric-grid">
    <div class="metric-card"><span class="metric-icon green">${icon('wallet')}</span><div><p>${esc(rangeLabel)}净收款</p><h2>¥${formatYuan(stat.net)}</h2><small>今日 ¥${formatYuan(stat.todayIncome)} · 收款 ¥${formatYuan(stat.income)} − 退款 ¥${formatYuan(stat.refund)}</small></div></div>
    <div class="metric-card"><span class="metric-icon blue">${icon('order')}</span><div><p>${esc(rangeLabel)}订单</p><h2>${stat.orders.length}</h2><small>${db.orders.filter((o) => o.status === 'PENDING_PAY').length} 笔等待付款（当前）</small></div></div>
    <div class="metric-card"><span class="metric-icon orange">${icon('refresh')}</span><div><p>${esc(rangeLabel)}退款</p><h2>¥${formatYuan(stat.refund)}</h2><small>以渠道成功为准，不计入柱子</small></div></div>
    <div class="metric-card"><span class="metric-icon red">${icon('alert')}</span><div><p>待处理异常（当前）</p><h2>${pendingAbn}</h2><small>需客服人工核实</small></div></div>
  </div>
  <div class="dashboard-grid">
    <section class="panel sales-panel">
      <div class="panel-heading">
        <div><h2>净收款走势</h2><p>${esc(rangeLabel)}按${rangeOf(trendRange).unit === 'month' ? '月' : '日'}聚合 · ${activeText}</p></div>
        <div class="range-tabs" role="tablist" aria-label="选择统计区间">
          ${STAT_RANGES.map((r) => `<button role="tab" aria-selected="${r.key === trendRange}" class="${r.key === trendRange ? 'active' : ''}" onclick="pickTrend('${r.key}')">${r.label}</button>`).join('')}
        </div>
      </div>
      <div class="chart-area"><div class="chart-y">${chartY}</div><div class="bars">${bars}</div></div>
      <div class="chart-legend"><span><i class="income"></i>净收款（收款 − 退款）</span><span>淡灰短条表示当天没有流水</span></div>
    </section>
    <section class="panel">
      <div class="panel-heading"><div><h2>库存健康度</h2><p>商品可售库存实时状态</p></div></div>
      <div class="stock-list">
        ${db.products.slice().sort((a, b) => a.available - b.available).slice(0, 6).map((p) => `
        <div class="${p.available <= 2 ? 'low' : ''}">
          <img src="${esc(p.image)}" alt=""/>
          <span><b>${esc(p.name)}</b><small>${esc(p.id)} · 已售 ${p.sales}</small></span>
          <div class="stock-value"><b>${p.available}</b><small>可售</small></div>
          <div class="stock-bar"><i style="width:${Math.min(100, p.total ? Math.round(p.available / p.total * 100) : 0)}%"></i></div>
        </div>`).join('')}
      </div>
      <div class="stock-note">${icon('alert', 17)} 按可售从少到多显示前 6 个（共 ${db.products.length} 个商品）${lowStock ? `，其中 ${lowStock} 个已低于 2 件` : ''}</div>
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
// 商品展示状态：已下架 > 已售罄 > 在售（与服务端 Product.StatusLabel 一致）
function productStatus(p) {
  if (!p.onSale) return '已下架';
  if (p.available <= 0) return '已售罄';
  return '在售';
}
function productTable(list) {
  return `
    <div class="data-table">
      <div class="tr th"><span>商品信息</span><span>价格</span><span>总库存</span><span>可售库存</span><span>状态</span><span>操作</span></div>
      ${list.length === 0 ? '<div class="empty-state">没有匹配的商品</div>' : ''}
      ${list.map((p) => `
      <div class="tr">
        <span class="product-cell"><img src="${esc(p.image)}" alt=""/><i><b>${esc(p.name)}</b><small>${esc(p.id)} · ${esc(p.category || '其他')} · 已售 ${p.sales}</small></i></span>
        <span>¥${formatYuan(p.priceFen)}</span>
        <span>${p.total}</span>
        <span><b>${p.available}</b> 件</span>
        <span><i class="dot ${p.onSale && p.available > 0 ? 'online' : ''}"></i>${productStatus(p)}</span>
        <span><button class="${p.onSale ? 'danger-link' : 'link-button'}" onclick="toggleSale('${esc(p.id)}',${!p.onSale})">${p.onSale ? '下架' : '上架'}</button></span>
      </div>`).join('')}
    </div>`;
}
function renderProductTable() {
  const q = productQuery.trim().toLowerCase();
  const list = db.products.filter((p) => !q || p.name.toLowerCase().includes(q)
    || p.id.toLowerCase().includes(q) || (p.category || '').toLowerCase().includes(q));
  const wrap = $('#productTableWrap');
  if (wrap) wrap.innerHTML = productTable(list);
}
// 新建商品：分类 + 从本机选择图片上传（不提供任何内置示例图/默认图）
const PRODUCT_CATEGORIES = ['家用电器', '电子产品', '儿童玩具', '书本', '食品', '家居生活', '其他'];
function openCreateProduct() {
  window._npFile = null;
  openModal(modalFrame('新建商品', 'bag', `
    <p class="form-note" style="margin-top:16px">创建后默认在售，可在列表中下架。金额以「元」输入、按「分」提交；商品图片从本机选择，支持 JPG / PNG / WEBP / GIF，单张不超过 5MB。</p>
    <div class="form-grid">
      <label>商品名称<input id="npName" placeholder="例如：便携保温杯"/></label>
      <label>商品分类<select id="npCategory">${PRODUCT_CATEGORIES.map((c) => `<option value="${c}">${c}</option>`).join('')}</select></label>
      <label>价格（元）<input id="npPrice" type="number" min="0.01" step="0.01" value="100"/></label>
      <label>库存（件）<input id="npStock" type="number" min="0" step="1" value="5"/></label>
    </div>
    <div class="img-field">
      <label>商品图片</label>
      <input id="npFile" type="file" accept="image/png,image/jpeg,image/webp,image/gif"
        style="display:none" onchange="previewLocalImage(this)"/>
      <div class="img-pick">
        <button type="button" class="ghost" onclick="document.getElementById('npFile').click()">选择本地图片</button>
        <small id="npFileName">未选择文件</small>
      </div>
      <div class="img-preview">
        <img id="npPreview" alt="商品图片预览" hidden/>
        <span id="npPlaceholder">尚未选择图片</span>
      </div>
    </div>
    <button class="primary wide" id="npSubmit" onclick="submitCreateProduct()">确认创建</button>`));
}
function previewLocalImage(input) {
  const file = (input.files && input.files[0]) || null;
  window._npFile = file;
  const nameEl = $('#npFileName');
  const img = $('#npPreview');
  const ph = $('#npPlaceholder');
  if (!file) {
    if (nameEl) nameEl.textContent = '未选择文件';
    if (img) { img.hidden = true; img.removeAttribute('src'); }
    if (ph) ph.hidden = false;
    return;
  }
  if (nameEl) nameEl.textContent = file.name;
  if (img) { img.src = URL.createObjectURL(file); img.hidden = false; }
  if (ph) ph.hidden = true;
}
async function uploadProductImage(file) {
  const fd = new FormData();
  fd.append('file', file);
  const data = await api('/api/uploads', { method: 'POST', body: fd });
  if (!data || !data.url) throw new Error('上传失败：服务端未返回图片地址');
  return data.url;
}
async function submitCreateProduct() {
  const name = ($('#npName') && $('#npName').value.trim()) || '';
  const category = ($('#npCategory') && $('#npCategory').value) || '其他';
  const price = parseFloat(($('#npPrice') && $('#npPrice').value) || '0');
  const stock = parseInt(($('#npStock') && $('#npStock').value) || '-1', 10);
  const file = window._npFile;
  if (!name) { toast('请填写商品名称'); return; }
  if (!Number.isFinite(price) || price <= 0) { toast('请填写有效价格'); return; }
  if (!Number.isInteger(stock) || stock < 0) { toast('库存必须为非负整数'); return; }
  if (!file) { toast('请选择商品图片'); return; }
  if (file.size > 5 * 1024 * 1024) { toast('图片不能超过 5MB'); return; }
  const btn = $('#npSubmit');
  if (btn) { btn.disabled = true; btn.textContent = '图片上传中…'; }
  try {
    const image = await uploadProductImage(file);
    const priceFen = Math.round(price * 100);
    window._npFile = null;
    await act(() => api('/api/products', { method: 'POST', body: { name, image, category, priceFen, stock } }), '商品已创建并上架');
  } catch (e) {
    toast('创建失败：' + e.message, 3200);
    if (btn) { btn.disabled = false; btn.textContent = '确认创建'; }
  }
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
  // 导出同样只导资金流水，历史库里的商品动作不进入对账文件
  const list = (db.ledger || []).filter((l) => !/^商品/.test(l.type || ''));
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

/* ---------- 仓库：发货管理 / 库存管理 ---------- */
let shipQuery = '';
function warehouseList() {
  const q = shipQuery.trim().toLowerCase();
  return db.orders
    .filter((o) => o.status === 'PENDING_SHIP')
    .filter((o) => !q || (o.id || '').toLowerCase().includes(q)
      || (o.productName || '').toLowerCase().includes(q)
      || (o.buyerName || '').toLowerCase().includes(q));
}
function warehouseCardsHTML() {
  const list = warehouseList();
  if (list.length === 0) {
    return `<div class="empty-state">${shipQuery ? '没有匹配「' + esc(shipQuery) + '」的待发货订单' : '暂无待发货订单'}</div>`;
  }
  return list.map((o) => `
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
      </article>`).join('');
}
// 搜索只重绘卡片区，避免整页重绘导致输入框失焦
function renderShipGrid() {
  const grid = document.getElementById('shipGrid');
  if (grid) grid.innerHTML = warehouseCardsHTML();
  const cnt = document.getElementById('shipCount');
  if (cnt) cnt.textContent = `共 ${warehouseList().length} 笔合法待处理订单${shipQuery ? '（已按关键词筛选）' : ''}`;
}
function renderWarehouse() {
  return `
  <div class="info-banner">${icon('shield')}<div><b>发货状态校验已启用</b><p>仅「待发货」订单可登记一次发货；退款审核中的订单已自动从列表移除，重复登记会被后端拒绝。</p></div></div>
  <section class="panel full-panel">
    <div class="table-toolbar">
      <div><h2>待发货任务</h2><p id="shipCount">共 ${warehouseList().length} 笔合法待处理订单${shipQuery ? '（已按关键词筛选）' : ''}</p></div>
      <div class="small-search">${icon('search', 16)}
        <input value="${esc(shipQuery)}" placeholder="搜索订单号 / 商品 / 收件人" aria-label="搜索订单"
          oninput="shipQuery=this.value;renderShipGrid()"/>
      </div>
    </div>
    <div class="fulfillment-grid" id="shipGrid">${warehouseCardsHTML()}</div>
  </section>`;
}

// 库存管理：给商品添加库存（总库存 + 可售同时增加）
let stockQuery = '';
function stockList() {
  const q = stockQuery.trim().toLowerCase();
  return db.products.filter((p) => !q || (p.name || '').toLowerCase().includes(q) || (p.id || '').toLowerCase().includes(q));
}
function stockRowsHTML() {
  const list = stockList();
  if (list.length === 0) return '<div class="empty-state">没有匹配的商品</div>';
  return `
    <div class="tr th"><span>商品</span><span>编号</span><span>单价</span><span>总库存</span><span>可售</span><span>已售</span><span>补货数量</span><span>操作</span></div>
    ${list.map((p) => `
    <div class="tr">
      <span class="stock-name"><img src="${esc(p.image)}" alt=""/><b>${esc(p.name)}</b></span>
      <span class="mono">${esc(p.id)}</span>
      <span>¥${formatYuan(p.priceFen)}</span>
      <span>${p.total}</span>
      <span>${p.available <= 2 ? `<b class="danger-text">${p.available}</b>` : p.available}</span>
      <span>${p.sales}</span>
      <span><input class="stock-qty" id="qty-${esc(p.id)}" type="number" min="1" max="9999" step="1" value="10" aria-label="补货数量"/></span>
      <span><button class="primary" onclick="addStock('${esc(p.id)}')">添加库存</button></span>
    </div>`).join('')}`;
}
function renderStockTable() {
  const box = document.getElementById('stockTable');
  if (box) box.innerHTML = stockRowsHTML();
}
function renderInventory() {
  return `
  <div class="info-banner">${icon('box')}<div><b>库存管理：给商品补充库存</b><p>添加后总库存与可售数量同时增加，补货动作记录在「商品记录」里；下单扣减与补货共用同一把写事务，不会出现可售大于总库存。</p></div></div>
  <section class="panel full-panel">
    <div class="table-toolbar">
      <div><h2>商品库存</h2><p>共 ${db.products.length} 个商品，可售低于 2 件会标红</p></div>
      <div class="small-search">${icon('search', 16)}
        <input value="${esc(stockQuery)}" placeholder="搜索商品名称 / 编号" aria-label="搜索商品"
          oninput="stockQuery=this.value;renderStockTable()"/>
      </div>
    </div>
    <div class="data-table stock-table" id="stockTable">${stockRowsHTML()}</div>
  </section>`;
}
async function addStock(pid) {
  const el = document.getElementById('qty-' + pid);
  const qty = el ? Number(el.value) : 0;
  if (!Number.isInteger(qty) || qty <= 0) { toast('请输入大于 0 的整数数量'); return; }
  if (qty > 9999) { toast('单次最多添加 9999 件'); return; }
  if (busy) { toast('操作进行中，请勿重复点击'); return; }
  busy = true;
  try {
    const p = await api(`/api/products/${pid}/stock`, { method: 'POST', body: { qty } });
    toast(`已为「${p.name}」添加 ${qty} 件，当前可售 ${p.available} 件`);
    await refresh(); render();
  } catch (e) {
    toast('添加库存失败：' + e.message, 3200);
    await refresh(); render();
  } finally { busy = false; }
}
function shipOrder(id) {
  act(() => api(`/api/orders/${id}/ship`, { method: 'POST', body: { carrier: '顺丰速运', trackingNo: '' } }),
    '发货登记成功，该订单不可重复发货');
}
function renderShipments() {
  return `
  <section class="panel full-panel">
    <div class="table-toolbar"><div><h2>已登记发货</h2><p>发货记录对订单唯一（共 ${db.shipments.length} 条），签收情况由买家确认收货回写</p></div></div>
    <div class="data-table ledger-table shipment-table">
      <div class="tr th"><span>发货单号</span><span>关联订单</span><span>承运方</span><span>运单号</span><span>发货时间</span><span>操作人</span><span>签收情况</span></div>
      ${db.shipments.length === 0 ? '<div class="empty-state">暂无发货记录</div>' : ''}
      ${db.shipments.map((sh) => `
      <div class="tr">
        <span class="mono">${esc(sh.id)}</span>
        <span class="mono">${esc(sh.orderId)}</span>
        <span>${esc(sh.carrier)}</span>
        <span class="mono">${esc(sh.trackingNo)}</span>
        <span>${fmtTime(sh.createdAt)}</span>
        <span>${esc(sh.operator || '-')}</span>
        <span>${sh.signedAt ? `已签收 · ${fmtTime(sh.signedAt)}` : '<i class="dot"></i>运输中'}</span>
      </div>`).join('')}
    </div>
  </section>`;
}
// 签收记录：只列已签收（买家确认收货）的发货单，用于仓库核对签收时间
function renderSignRecords() {
  const signed = db.shipments.filter((sh) => sh.signedAt);
  return `
  <div class="info-banner">${icon('check')}<div><b>签收记录来自买家「确认收货」</b><p>买家在订单里点确认收货后，订单状态变为已签收，同时把签收时间与签收人回写到这里的发货记录。</p></div></div>
  <section class="panel full-panel">
    <div class="table-toolbar"><div><h2>货物签收记录</h2><p>共 ${signed.length} 笔已签收（待签收 ${db.shipments.length - signed.length} 笔）</p></div></div>
    <div class="data-table ledger-table">
      <div class="tr th"><span>发货单号</span><span>关联订单</span><span>承运方</span><span>运单号</span><span>签收时间</span><span>签收人</span></div>
      ${signed.length === 0 ? '<div class="empty-state">还没有买家签收</div>' : ''}
      ${signed.map((sh) => `
      <div class="tr">
        <span class="mono">${esc(sh.id)}</span>
        <span class="mono">${esc(sh.orderId)}</span>
        <span>${esc(sh.carrier)}</span>
        <span class="mono">${esc(sh.trackingNo)}</span>
        <span>${fmtTime(sh.signedAt)}</span>
        <span>${esc(sh.signedBy || '-')}</span>
      </div>`).join('')}
    </div>
  </section>`;
}

/* ---------- 运营：商品操作记录 ---------- */
// 与资金流水刻意分开：资金流水只记钱，商品新增 / 上下架动作记在这里
function renderProductOps() {
  const list = (db.productOps || []).slice().reverse();
  return `
  <div class="info-banner">${icon('bag')}<div><b>商品操作与资金流水分离</b><p>这里只记录商品的「新增商品 / 商品上架 / 商品下架」，金额相关的收款与退款请到「资金流水」查看。</p></div></div>
  <section class="panel full-panel">
    <div class="table-toolbar"><div><h2>商品操作记录</h2><p>只增不改，共 ${list.length} 条</p></div></div>
    <div class="data-table ledger-table">
      <div class="tr th"><span>记录号</span><span>操作类型</span><span>商品编号</span><span>商品名称</span><span>操作人</span><span>操作时间</span></div>
      ${list.length === 0 ? '<div class="empty-state">暂无商品操作记录</div>' : ''}
      ${list.map((r) => `
      <div class="tr">
        <span class="mono">${esc(r.id)}</span>
        <span><i class="ledger-dot ${esc(r.type)}"></i>${esc(r.type)}</span>
        <span class="mono">${esc(r.productId || '-')}</span>
        <span>${esc(r.name || '-')}</span>
        <span>${esc(r.operator || '-')}</span>
        <span>${fmtTime(r.createdAt)}</span>
      </div>`).join('')}
    </div>
  </section>`;
}

/* ---------- 管理员：B 端员工账号 ---------- */
const STAFF_ROLE_LABEL = { ops: '运营专员', warehouse: '仓库管理员', cs: '售后客服', admin: '管理员' };
function renderStaffAdmin() {
  const list = db.staff || [];
  return `
  <div class="info-banner">${icon('shield')}<div><b>管理员专属：维护 B 端员工账号</b><p>新建账号只能登录 B 端工作台，角色决定登录后可见的菜单；管理员可查看全部 B 端数据，但看不到 C 端买家数据。</p></div></div>
  <div class="dashboard-grid" style="grid-template-columns:1.35fr 1fr">
    <section class="panel">
      <div class="panel-heading"><div><h2>员工账号</h2><p>共 ${list.length} 个 B 端账号</p></div></div>
      <div class="data-table staff-table">
        <div class="tr th"><span>姓名</span><span>登录手机号</span><span>角色</span></div>
        ${list.length === 0 ? '<div class="empty-state">暂无员工账号</div>' : ''}
        ${list.map((a) => `
        <div class="tr">
          <span><b>${esc(a.name)}</b></span>
          <span class="mono">${esc(a.phone || '-')}</span>
          <span><i class="role-chip ${esc(a.role)}">${esc(STAFF_ROLE_LABEL[a.role] || a.role)}</i></span>
        </div>`).join('')}
      </div>
    </section>
    <section class="panel">
      <div class="panel-heading"><div><h2>新增 B 端账号</h2><p>创建后即可用手机号 + 初始密码登录</p></div></div>
      <div class="form-grid">
        <label>姓名<input id="stName" maxlength="20" placeholder="如 王仓管"/></label>
        <label>手机号<input id="stPhone" maxlength="11" inputmode="numeric" placeholder="11 位手机号"/></label>
        <label>初始密码<input id="stPwd" type="password" maxlength="32" placeholder="至少 6 位"/></label>
        <label>角色
          <select id="stRole">
            <option value="ops">运营专员</option>
            <option value="warehouse">仓库管理员</option>
            <option value="cs">售后客服</option>
            <option value="admin">管理员</option>
          </select>
        </label>
      </div>
      <p class="form-note" style="margin-top:12px">账号手机号不可重复；员工改角色后旧登录态立即失效，需要重新登录。</p>
      <button class="primary wide" onclick="createStaff()">创建账号</button>
    </section>
  </div>`;
}
async function createStaff() {
  const name = (($('#stName') || {}).value || '').trim();
  const phone = (($('#stPhone') || {}).value || '').trim();
  const password = ($('#stPwd') || {}).value || '';
  const role = (($('#stRole') || {}).value || 'ops');
  if (!name) { toast('请填写姓名'); return; }
  if (!/^\d{11}$/.test(phone)) { toast('手机号必须是 11 位数字'); return; }
  if (password.length < 6) { toast('初始密码至少 6 位'); return; }
  if (busy) { toast('操作进行中，请勿重复点击'); return; }
  busy = true;
  try {
    const r = await api('/api/staff', { method: 'POST', body: { name, phone, password, role } });
    toast(`已创建${STAFF_ROLE_LABEL[r.user.role] || ''}账号：${r.user.phone} / 初始密码已设置`);
    await refresh(); render();
  } catch (e) {
    toast('创建失败：' + e.message, 3200);
  } finally { busy = false; }
}

/* ---------- 客服：会话列表 ---------- */
// 未读口径：只有「最后一条消息是买家发的，且客服打开后的新消息」才算未读，
// 客服回复完（最后一条变成客服自己）徽标立刻清零，不再拿会话总数当提醒。
function chatReadKey() { return 'sco:chatread:' + (current ? current.id : ''); }
function chatReadMap() {
  try { return JSON.parse(localStorage.getItem(chatReadKey()) || '{}'); } catch (e) { return {}; }
}
function markChatRead(orderId, at) {
  const m = chatReadMap();
  m[orderId] = at || new Date().toISOString();
  try { localStorage.setItem(chatReadKey(), JSON.stringify(m)); } catch (e) { /* 存储不可用时仅影响徽标 */ }
}
function myChatRole() { return current && current.role === 'cs' ? 'cs' : 'buyer'; }
function threadUnread(t) {
  // 未读 = 「对方」的最后一条消息我还没看过：客服看买家发言，买家看客服回复
  if (!t || t.lastFromRole === myChatRole()) return false;
  const readAt = chatReadMap()[t.orderId];
  if (!readAt) return true;
  return new Date(t.lastAt).getTime() > new Date(readAt).getTime();
}
function unreadChatCount() { return (db.threads || []).filter(threadUnread).length; }

/* 买家侧弹提醒：客服回复后 8 秒轮询拉到新消息就 toast 一次；
   登录时静默登记已有未读，避免每次登录都被历史消息轰炸。 */
let chatToastSeen = {};
function syncBuyerChatNotice(silent) {
  if (!current || current.role !== 'buyer') return;
  (db.threads || []).forEach((t) => {
    if (!threadUnread(t)) { delete chatToastSeen[t.orderId]; return; }
    if (chatToastSeen[t.orderId] === t.lastAt) return;   // 这条已经提醒过
    chatToastSeen[t.orderId] = t.lastAt;
    if (!silent) toast(`客服回复了订单 ${t.orderId} 的咨询，请在订单卡里点「客服有新回复」查看`, 4200);
  });
}
// 不重绘整页，只更新侧边栏徽标，避免聊天时页面闪动
function updateChatBadge() {
  const el = document.querySelector('[data-badge="客服消息"]');
  if (!el) return;
  const n = unreadChatCount();
  el.textContent = n || '';
  el.hidden = !n;
}
function renderCsChats() {
  const list = db.threads || [];
  return `
  <div class="info-banner">${icon('headset')}<div><b>买家可在订单里「联系客服」</b><p>买家发送的订单卡片会带上订单号、商品、金额与当时状态，点开即可查看订单详情并回复。</p></div></div>
  <section class="panel full-panel">
    <div class="table-toolbar"><div><h2>客服消息</h2><p>按最后一条消息时间排序（共 ${list.length} 个会话）</p></div></div>
    <div class="chat-thread-list">
      ${list.length === 0 ? '<div class="empty-state">还没有买家发起咨询</div>' : ''}
      ${list.map((t) => `
      <button class="chat-thread ${threadUnread(t) ? 'unread' : ''}" onclick="openOrderChat('${esc(t.orderId)}')">
        <div class="chat-thread-avatar">${esc((t.buyerName || '买')[0])}</div>
        <div class="chat-thread-main">
          <div class="chat-thread-head"><b>${esc(t.buyerName || '买家')}</b>${threadUnread(t) ? '<i class="unread-dot"></i>' : ''}<small>${fmtRelTime(t.lastAt)}</small></div>
          <p>${t.lastKind === 'order' ? '［订单卡片］' : esc(t.lastText || '')}</p>
          <span class="mono">${esc(t.orderId)} · ${esc(t.productName || '')} · ${esc(stOf(t.orderStatus).label)}</span>
        </div>
        <em class="${threadUnread(t) ? 'wait' : ''}">${threadUnread(t) ? '待回复' : t.msgCount}</em>
      </button>`).join('')}
    </div>
  </section>`;
}

/* ---------- 订单客服会话（买家 / 客服共用） ---------- */
let chatOrder = null;
let chatTimer = null;
function stopChatPoll() {
  if (chatTimer) { clearInterval(chatTimer); chatTimer = null; }
  chatOrder = null;
}
function openOrderChat(orderId) {
  stopChatPoll();
  const o = orderFor(orderId);
  openModal(modalFrame('联系客服', 'headset', `
    <div class="chat-meta">
      <img src="${esc(o.productImage || '')}" alt=""/>
      <div><b>${esc(o.productName || '订单')}</b><small class="mono">${esc(orderId)}</small></div>
      <span>${badge(o.status)}</span>
    </div>
    <div class="chat-body" id="chatBody"><div class="empty-state">加载中…</div></div>
    <div class="chat-compose">
      <textarea id="chatInput" rows="2" placeholder="描述你的问题，点发送即可"></textarea>
      <div class="chat-actions">
        <button class="ghost" onclick="sendOrderCard()">${icon('order', 15)} 发送订单卡片</button>
        <button class="primary" onclick="sendChatText()">发送</button>
      </div>
    </div>`));
  chatOrder = orderId; // openModal 内部会 closeModal（顺带停轮询），因此必须在打开之后再赋值
  loadChat();
  chatTimer = setInterval(loadChat, 4000);
}
function chatBubble(m) {
  const mine = current && m.fromId === current.id;
  if (m.kind === 'order') {
    let c = {};
    try { c = JSON.parse(m.text); } catch (e) { c = {}; }
    const oid = c.orderId || m.orderId;
    return `<div class="chat-row ${mine ? 'mine' : ''}">
      <div class="chat-order-card">
        <img src="${esc(c.productImage || '')}" alt=""/>
        <div><b>${esc(c.productName || '订单')}</b>
          <small class="mono">${esc(oid)} · ¥${formatYuan(c.amountFen)} × ${c.qty || 1}</small>
          <em>${esc(c.statusLabel || '')}</em></div>
        <button class="link-button" onclick="openOrderDetail('${esc(oid)}')">查看订单</button>
      </div>
      <span class="chat-time">${esc(m.fromName)} · ${fmtRelTime(m.createdAt)}</span></div>`;
  }
  return `<div class="chat-row ${mine ? 'mine' : ''}">
    <div class="chat-bubble">${esc(m.text)}</div>
    <span class="chat-time">${esc(m.fromName)} · ${fmtRelTime(m.createdAt)}</span></div>`;
}
async function loadChat() {
  if (!chatOrder) return;
  const box = $('#chatBody');
  if (!box) { stopChatPoll(); return; }
  try {
    const msgs = await api(`/api/chat/${chatOrder}`);
    box.innerHTML = (msgs && msgs.length)
      ? msgs.map(chatBubble).join('')
      : '<div class="empty-state">还没有消息，点下方「发送订单卡片」把订单发给客服</div>';
    box.scrollTop = box.scrollHeight;
    // 打开即视为已读：用最后一条消息的服务端时间做水位，避免本机与服务器时钟偏差导致永远「未读」
    const lastAt = (msgs && msgs.length) ? msgs[msgs.length - 1].createdAt : new Date().toISOString();
    markChatRead(chatOrder, lastAt);
    updateChatBadge();
  } catch (e) { /* 网络抖动时保持当前内容 */ }
}
async function sendChatText() {
  const input = $('#chatInput');
  const text = ((input && input.value) || '').trim();
  if (!text) { toast('请先输入消息内容'); return; }
  if (!chatOrder) return;
  try {
    await api(`/api/chat/${chatOrder}`, { method: 'POST', body: { kind: 'text', text } });
    if (input) input.value = '';
    await loadChat();
    // 回复后最后一条消息变成客服自己，会话列表与徽标同步刷新
    if (current && current.role === 'cs') { await refresh(); updateChatBadge(); }
  } catch (e) { toast('发送失败：' + e.message, 3200); }
}
async function sendOrderCard() {
  if (!chatOrder) return;
  try {
    await api(`/api/chat/${chatOrder}`, { method: 'POST', body: { kind: 'order' } });
    await loadChat();
    toast('订单卡片已发送');
  } catch (e) { toast('发送失败：' + e.message, 3200); }
}

/* ---------- 商品评价（父子两级） ---------- */
async function openReviews(productId, orderId) {
  openModal(modalFrame('商品评价', 'user', `<div id="reviewWrap"><div class="empty-state">加载中…</div></div>`));
  await loadReviews(productId, orderId || '');
}
async function loadReviews(productId, orderId) {
  const wrap = $('#reviewWrap');
  if (!wrap) return;
  let list = [];
  try { list = (await api(`/api/products/${productId}/reviews`)) || []; } catch (e) { list = []; }
  const tops = list.filter((r) => !r.parentId).slice().reverse();
  const order = orderId ? db.orders.find((o) => o.id === orderId) : null;
  const canPost = !!(order && order.status === 'SIGNED');
  const reviewed = tops.some((r) => r.orderId === orderId);
  const repliesOf = (id) => list.filter((r) => r.parentId === id);
  const replyBox = (id) => (canPost ? `
    <div class="review-reply">
      <textarea id="reply-${esc(id)}" rows="2" placeholder="回复这条评价…"></textarea>
      <button class="secondary" onclick="submitReview('${esc(productId)}','${esc(orderId)}','${esc(id)}')">回复</button>
    </div>` : '');
  wrap.innerHTML = `
    ${tops.length ? tops.map((r) => `
      <article class="review-item">
        <div class="review-head"><b>${esc(r.buyerName)}</b><small>${fmtRelTime(r.createdAt)} · ${esc(r.orderId)}</small></div>
        <p>${esc(r.content)}</p>
        ${repliesOf(r.id).map((c) => `
          <div class="review-reply-item"><b>${esc(c.buyerName)}</b><span>${esc(c.content)}</span><small>${fmtRelTime(c.createdAt)}</small></div>`).join('')}
        ${replyBox(r.id)}
      </article>`).join('') : '<div class="empty-state">这件商品还没有评价</div>'}
    ${canPost && !reviewed ? `
      <div class="review-form">
        <textarea id="reviewInput" rows="3" placeholder="说说这件商品的使用感受…"></textarea>
        <button class="primary wide" onclick="submitReview('${esc(productId)}','${esc(orderId)}','')">发表评价</button>
      </div>` : ''}
    ${!orderId ? '<div class="form-note">只有已签收订单的买家可以评价或回复。</div>' : ''}`;
}
async function submitReview(productId, orderId, parentId) {
  const el = parentId ? $('#reply-' + parentId) : $('#reviewInput');
  const content = ((el && el.value) || '').trim();
  if (!content) { toast('请先填写评价内容'); return; }
  try {
    await api(`/api/products/${productId}/reviews`, {
      method: 'POST', body: { orderId, parentId, content },
    });
    toast(parentId ? '回复已发布' : '评价已发布');
    await loadReviews(productId, orderId);
  } catch (e) { toast('发布失败：' + e.message, 3200); }
}

/* ---------- 客服：退款审核 ---------- */
function orderFor(orderId) { return db.orders.find((o) => o.id === orderId) || {}; }
function renderRefundReview() {
  // 退款审核只保留「还需要客服动作」的单子：已成功的退款移到「处理记录」，避免重复审核
  const pending = db.refunds.filter((r) => ['PENDING', 'AWAITING', 'FAILED'].includes(r.status));
  const done = db.refunds.length - pending.length;
  return `
  <div class="info-banner warm">${icon('alert')}<div><b>审核后将触发明确的状态迁移</b><p>同意并经渠道退款成功后恢复库存；拒绝后订单回到待发货状态。售前退款恢复库存，迟到支付退款不恢复。</p></div></div>
  <div class="review-list">
    ${pending.length === 0 ? `<div class="empty-state">暂无待处理的退款申请${done ? `，已处理的 ${done} 笔可在「处理记录」查看` : ''}</div>` : ''}
    ${pending.map((r) => {
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
  // 已处理的异常支付同样移入「处理记录」，这里只留待人工核实
  const pending = db.abnormal.filter((a) => a.status === 'PENDING');
  const done = db.abnormal.length - pending.length;
  return `
  <div class="info-banner danger-banner">${icon('alert')}<div><b>迟到支付不会复活已关闭订单</b><p>人工核实后原路退款，不重新占用或恢复已在关单时释放的库存。</p></div></div>
  <div class="review-list">
    ${pending.length === 0 ? `<div class="empty-state">暂无待处理的异常支付${done ? `，已处理的 ${done} 笔可在「处理记录」查看` : ''}</div>` : ''}
    ${pending.map((a) => {
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
/* ---------- 客服：处理记录（已完成退款 / 已处理异常支付） ---------- */
// 退款成功或拒绝、异常支付核实完毕后都会离开待办列表，处理结果在这里留档：
// 单号、订单、金额、处理结果、处理时间、处理备注，便于事后追溯。
function renderCsRecords() {
  const refunds = db.refunds
    .filter((r) => ['SUCCEEDED', 'REJECTED'].includes(r.status))
    .sort((a, b) => new Date(b.updatedAt || b.createdAt) - new Date(a.updatedAt || a.createdAt));
  const abnormals = db.abnormal
    .filter((a) => a.status === 'HANDLED')
    .sort((a, b) => new Date(b.handledAt || b.createdAt) - new Date(a.handledAt || a.createdAt));
  return `
  <div class="info-banner">${icon('order')}<div><b>已处理单据留档</b><p>退款成功 / 拒绝退款、异常支付核实退款后会自动从待办列表移到这里，记录处理结果与时间。</p></div></div>
  <section class="panel full-panel">
    <div class="table-toolbar"><div><h2>退款处理记录</h2><p>共 ${refunds.length} 笔</p></div></div>
    <div class="data-table ledger-table record-table">
      <div class="tr th"><span>退款单号</span><span>关联订单</span><span>买家</span><span>金额</span><span>处理结果</span><span>处理时间</span><span>处理备注</span></div>
      ${refunds.length === 0 ? '<div class="empty-state">暂无退款处理记录</div>' : ''}
      ${refunds.map((r) => {
        const o = orderFor(r.orderId);
        return `<div class="tr">
          <span class="mono">${esc(r.id)}</span>
          <span class="mono">${esc(r.orderId)}</span>
          <span>${esc(o.buyerName || '-')}</span>
          <span>¥${formatYuan(r.amountFen)}</span>
          <span><i class="ledger-dot ${r.status === 'SUCCEEDED' ? '退款' : '退款拒绝'}"></i>${esc((REFUND_STATUS[r.status] || { label: r.status }).label)}</span>
          <span>${fmtTime(r.updatedAt || r.createdAt)}</span>
          <span>${esc(r.reviewNote || (r.status === 'SUCCEEDED' ? '渠道退款成功' : '已拒绝申请'))}</span>
        </div>`;
      }).join('')}
    </div>
  </section>
  <section class="panel full-panel">
    <div class="table-toolbar"><div><h2>异常支付处理记录</h2><p>共 ${abnormals.length} 笔</p></div></div>
    <div class="data-table ledger-table record-table">
      <div class="tr th"><span>异常单号</span><span>关联订单</span><span>渠道交易号</span><span>金额</span><span>处理结果</span><span>处理时间</span><span>处理备注</span></div>
      ${abnormals.length === 0 ? '<div class="empty-state">暂无异常支付处理记录</div>' : ''}
      ${abnormals.map((a) => `<div class="tr">
          <span class="mono">${esc(a.id)}</span>
          <span class="mono">${esc(a.orderId)}</span>
          <span class="mono">${esc(a.channelTxnId || '-')}</span>
          <span>¥${formatYuan(a.amountFen)}</span>
          <span><i class="ledger-dot 退款"></i>已原路退款</span>
          <span>${fmtTime(a.handledAt || a.createdAt)}</span>
          <span>${esc(a.handleNote || a.reason || '-')}</span>
        </div>`).join('')}
    </div>
  </section>`;
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

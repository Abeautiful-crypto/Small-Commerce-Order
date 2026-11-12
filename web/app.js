/* 多多交易 · 订单交易系统前端
 * 视觉与信息架构来自「拼多多风格原型.html」，所有数据改为调用后端真实接口。
 * 金额单位：分（int），前端仅做展示换算，判定与计算全部在服务端。
 */
const ROLES = {
  buyer: { name: '买家', ico: '🛒', desc: '逛店、下单、支付、取消、申请退款（仅自己的订单）' },
  ops: { name: '运营', ico: '📊', desc: '商品上下架、看库存、对账核查（不能改订单状态）' },
  warehouse: { name: '仓库', ico: '📦', desc: '查待发货、登记发货（一次性）' },
  cs: { name: '客服', ico: '🎧', desc: '审核退款、处理迟到支付（异常）' },
};
const STATUS = {
  PENDING_PAY: { label: '待付款', cls: 'pending', side: '库存已占用，10 分钟内未付款将自动关闭并释放' },
  CLOSED: { label: '已关闭', cls: 'closed', side: '占用已释放一次，重复关闭不会重复释放' },
  PENDING_SHIP: { label: '待发货', cls: 'ship', side: '已收款，库存仍占用，等待仓库登记发货' },
  REFUNDING: { label: '退款中', cls: 'refunding', side: '申请退款即冻结发货动作' },
  SHIPPED: { label: '已发货', cls: 'paid', side: '占用转消耗，已发货不受理售前退款' },
  REFUNDED: { label: '已退款', cls: 'refunded', side: '渠道确认成功；售前恢复库存，迟到支付不恢复' },
  ABNORMAL_PAY: { label: '异常支付', cls: 'abnormal', side: '关闭后迟到的成功通知，不复活订单、不重占库存' },
};
const REFUND_STATUS = {
  PENDING: { label: '待客服审核', cls: 'pending' },
  AWAITING: { label: '等待渠道确认', cls: 'ship' },
  FAILED: { label: '渠道失败·可重试', cls: 'abnormal' },
  SUCCEEDED: { label: '退款成功', cls: 'refunded' },
  REJECTED: { label: '已拒绝', cls: 'closed' },
};
const FALLBACK_USERS = [
  { id: 'u001', name: '张小买', role: 'buyer' }, { id: 'u002', name: '李小购', role: 'buyer' },
  { id: 'u003', name: '王小单', role: 'buyer' }, { id: 'admin', name: '运营专员', role: 'ops' },
  { id: 'wh', name: '仓库管理员', role: 'warehouse' }, { id: 'cs', name: '客服小花', role: 'cs' },
];

let current = null;
let meta = null;
let buyerTab = 'shop';
let db = {
  products: [], orders: [], refunds: [], abnormal: [], ledger: [],
  notifyResults: [], shipments: [], payments: [], summary: null,
};

const $ = (s) => document.querySelector(s);
// 金额展示层换算：后端 int64「分」→ ¥xx.xx，前端不参与任何金额算术
const formatYuan = (f) => (Number(f || 0) / 100).toFixed(2);
const esc = (v) => String(v == null ? '' : v).replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const stOf = (s) => STATUS[s] || { label: s || '-', cls: 'closed', side: '' };

function fmtTime(t) {
  if (!t) return '-';
  const d = new Date(t);
  if (isNaN(d.getTime())) return String(t);
  const p = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}
function toast(msg, ms) {
  const t = $('#toast');
  t.textContent = msg;
  t.style.display = 'block';
  clearTimeout(t._t);
  t._t = setTimeout(() => { t.style.display = 'none'; }, ms || 2200);
}
function mask(html) {
  const m = document.createElement('div');
  m.className = 'mask';
  m.innerHTML = html;
  m.onclick = (e) => { if (e.target === m) m.remove(); };
  document.body.appendChild(m);
  return m;
}
function closeMask() { document.querySelectorAll('.mask').forEach((m) => m.remove()); }

/* 前端 requireRole 守卫（.agent/frontend/permission.md §2）：
 * 所有写操作在发起请求前统一过守卫；越权演示用 skipGuard 绕过前端守卫，
 * 以便真实调用后端并取得 403 硬证据。后端仍独立校验，前端守卫只是体验层。 */
const ROUTE_GUARD = [
  { re: /^\/api\/orders$/, roles: ['buyer'], action: '下单/支付' },
  { re: /^\/api\/orders\/[^/]+\/(cancel|expire|pay-intent|refund-apply)$/, roles: ['buyer'], action: '下单/支付/取消/申请退款' },
  { re: /^\/api\/orders\/[^/]+\/ship$/, roles: ['warehouse'], action: '登记发货' },
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
  mask(`<div class="sheet"><div class="deny">
    <div class="ic">⛔</div><div class="t1">无权访问（前端权限守卫）</div>
    <div class="t2">当前身份：<b>${current ? ROLES[current.role].name + ' / ' + esc(current.name) : '未登录'}</b><br/>
    操作「${esc(action)}」需要其它角色权限，<b>本次请求未发出，未做任何数据改动</b>。</div>
    <button class="submit" onclick="closeMask()">我知道了</button></div></div>`);
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
  const headers = { 'Content-Type': 'application/json' };
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
    err.data = json.data;
    throw err;
  }
  return json.data;
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
    const [refunds, abnormal, orders] = await Promise.all([
      api('/api/refunds'), api('/api/abnormal'), api('/api/orders'),
    ]);
    db.refunds = refunds || []; db.abnormal = abnormal || []; db.orders = orders || [];
  }
}

let busy = false;
async function act(fn, okMsg) {
  if (busy) { toast('操作进行中，请勿重复点击'); return null; }
  busy = true; // 请求进行中禁用重复提交，避免幂等按钮被连点
  try {
    const data = await fn();
    closeMask();
    if (okMsg) toast(okMsg);
    await refresh();
    render();
    return data;
  } catch (e) {
    toast(e.guarded ? e.message : '操作被拒绝：' + e.message, 3000);
    return null;
  } finally {
    busy = false;
  }
}

/* ================= 登录 ================= */
function usersOf(role) {
  const list = (meta && meta.users) || FALLBACK_USERS;
  return list.filter((u) => u.role === role);
}
function renderLogin() {
  const pwdless = !!(meta && meta.loginPasswordless);
  const pwdField = pwdless
    ? ''
    : `<div class="field"><label>登录密码</label><input id="pwdInput" type="password" autocomplete="current-password" placeholder="${(meta && meta.env === 'production') ? '生产模式：请输入账号密码' : '演示模式可留空'}"/></div>`;
  const modeNote = pwdless
    ? '<b>免密码登录</b>：选择角色与账号即可进入，登录态仍绑定服务端会话令牌。'
    : ((meta && meta.env === 'production')
      ? '<b>生产模式</b>：密码由服务端环境变量配置，登录态仅认会话令牌。'
      : '演示模式：选角色即登录，密码可留空。');
  $('#app').innerHTML = `
  <div id="login">
    <div class="login-hero"><h1>多多交易</h1><p>小型电商订单交易系统 · 前后端联通版</p></div>
    <div class="login-body">
      <div class="hint">请选择登录角色。本系统<b>严格按角色隔离权限</b>：每个角色只能操作职责范围内的功能，越权请求由后端直接返回 403 并拒绝一切数据改动（登录后页面底部可体验「越权演示」）。</div>
      <div class="role-grid" id="roleGrid"></div>
      <div class="field"><label>选择演示账号</label><select id="userSel"></select></div>
      ${pwdField}
      <button class="enter-btn" onclick="doLogin()">进 入 系 统</button>
      <div class="subtle">${modeNote}金额单位统一为「分」。</div>
    </div>
  </div>`;
  let selRole = 'buyer';
  const rg = $('#roleGrid');
  Object.entries(ROLES).forEach(([k, v]) => {
    const d = document.createElement('div');
    d.className = 'role-card' + (k === selRole ? ' sel' : '');
    d.innerHTML = `<div class="ico">${v.ico}</div><div class="nm">${v.name}</div><div class="ds">${v.desc}</div>`;
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
function emptyDb() {
  return {
    products: [], orders: [], refunds: [], abnormal: [], ledger: [],
    notifyResults: [], shipments: [], payments: [], summary: null,
  };
}
function doLogin() {
  const id = $('#userSel').value;
  const u = usersOf(window._selRole).find((x) => x.id === id);
  if (!u) { toast('请选择有效账号'); return; }
  (async () => {
    let token = '';
    const password = ($('#pwdInput') && $('#pwdInput').value) || '';
    try {
      const sess = await api('/api/session', { method: 'POST', body: { role: u.role, userId: u.id, password } });
      if (sess && sess.sessionEnabled && sess.token) token = sess.token;
    } catch (e) {
      if (meta && meta.env === 'production') { toast('登录失败：' + e.message, 3200); return; }
      // demo 模式下 Redis 不可用时降级为请求头身份
    }
    current = { role: u.role, id: u.id, name: u.name, token };
    db = emptyDb(); // 角色切换必须清空上一角色的本地状态，避免串数据
    buyerTab = 'shop';
    try { await refresh(); } catch (e) { toast('加载数据失败：' + e.message); }
    render();
    toast(`已以 ${ROLES[u.role].name}·${u.name} 身份登录${token ? '（Redis 会话）' : ''}`);
  })();
}
function doLogout() { current = null; db = emptyDb(); renderLogin(); }

/* ================= 主框架 ================= */
function render() {
  if (!current) { renderLogin(); return; }
  const top = `<div class="topbar"><div class="logo">多多交易</div><div class="spacer"></div>
    <div class="who">${ROLES[current.role].name}·${esc(current.name)}</div>
    <div class="exit" onclick="doLogout()">退出</div></div>`;
  let body = '';
  if (current.role === 'buyer') body = buyerTab === 'shop' ? renderShop() : renderMyOrders();
  else if (current.role === 'ops') body = renderOps();
  else if (current.role === 'warehouse') body = renderWarehouse();
  else if (current.role === 'cs') body = renderCs();
  const tab = current.role === 'buyer' ? renderTabbar() : '';
  $('#app').innerHTML = top + body + renderGuard() + tab;
}
function renderTabbar() {
  return `<div class="tabbar">
    <div class="tb ${buyerTab === 'shop' ? 'act' : ''}" onclick="switchTab('shop')"><span class="ic">🏠</span>首页</div>
    <div class="tb"><span class="ic">📂</span>分类</div>
    <div class="tb"><span class="ic">🛍️</span>购物车</div>
    <div class="tb ${buyerTab === 'order' ? 'act' : ''}" onclick="switchTab('order')"><span class="ic">👤</span>我的</div>
  </div>`;
}
function switchTab(t) { buyerTab = t; render(); }

/* ================= 买家：逛店 ================= */
function renderShop() {
  const cards = db.products.map((p) => `
    <div class="pcard">
      <img class="ph" src="${esc(p.image)}" alt="${esc(p.name)}"/>
      <div class="pb">
        <div class="ttl">${esc(p.name)}</div>
        <div class="pr"><span class="cur">¥</span><span class="num">${formatYuan(p.priceFen)}</span><span class="old">¥${formatYuan(p.oldPriceFen)}</span></div>
        <div class="meta">已拼${p.sales}件 · 剩${p.available}件${p.onSale ? '' : ' · 已下架'}</div>
        <div class="acts">
          <button class="btn-pin" ${p.onSale && p.available > 0 ? '' : 'disabled'} onclick="openOrder('${p.id}',2)">发起拼单</button>
          <button class="btn-buy" ${p.onSale && p.available > 0 ? '' : 'disabled'} onclick="openOrder('${p.id}',1)">单独购买</button>
        </div>
      </div>
    </div>`).join('');
  return `<div class="search">🔍 搜索 耳机 / 坚果 零食 …</div>
    <div class="banner">🎉 限时百亿补贴 · 全场 ¥${formatYuan(10000)} 起 · 下单即占库存，10 分钟未付款自动释放</div>
    <div class="grid">${cards}</div>`;
}
function openOrder(pid, qtyDefault) {
  const p = db.products.find((x) => x.id === pid);
  if (!p) return;
  if (!p.onSale) { toast('商品已下架'); return; }
  const qty = Math.min(qtyDefault, Math.max(1, p.available));
  const m = mask(`<div class="sheet">
    <span class="close-x" onclick="closeMask()">×</span><h3>确认订单</h3>
    <div class="row"><img src="${esc(p.image)}"/><div style="flex:1"><div>${esc(p.name)}</div>
      <div style="color:var(--pdd-red);font-weight:700">¥${formatYuan(p.priceFen)}</div></div></div>
    <div class="qty">数量
      <button onclick="chgQ(-1)">−</button><span class="n" id="qN">${qty}</span><button onclick="chgQ(1)">＋</button>
      <span style="color:#bbb;font-size:12px">（可售 ${p.available}）</span></div>
    <div style="display:flex;justify-content:space-between;font-size:14px;padding:8px 0">
      <span>合计</span><b style="color:var(--pdd-red)">¥<span id="qSum">${formatYuan(p.priceFen * qty)}</span></b></div>
    <div class="subtle">下单成功即刻占用可售数量，并保存此刻单价快照；后续调价不影响本单。</div>
    <button class="submit" onclick="submitOrder('${pid}')">提交订单</button>
  </div>`);
  m._p = p;
}
function chgQ(d) {
  const m = document.querySelector('.mask');
  const p = m && m._p;
  if (!p) return;
  let n = parseInt($('#qN').textContent, 10) + d;
  n = Math.max(1, Math.min(2, n));
  if (n > p.available) { toast('超出可售数量'); return; }
  $('#qN').textContent = n;
  $('#qSum').textContent = formatYuan(p.priceFen * n);
}
function submitOrder(pid) {
  const qty = parseInt($('#qN').textContent, 10);
  act(() => api('/api/orders', { method: 'POST', body: { productId: pid, qty } }), '下单成功，已占用库存');
  buyerTab = 'order';
}

/* ================= 买家：我的订单 ================= */
function renderMyOrders() {
  const rows = db.orders.map((o) => {
    const s = stOf(o.status);
    let acts = `<button class="mini-btn" onclick="viewOrder('${o.id}')">查看详情</button>`;
    if (o.status === 'PENDING_PAY') {
      acts = `<button class="mini-btn primary" onclick="payOrder('${o.id}')">去支付</button>
        <button class="mini-btn warn" onclick="cancelOrder('${o.id}')">取消订单</button>
        <button class="mini-btn" onclick="expireOrder('${o.id}')">模拟超时关闭</button>` + acts;
    } else if (o.status === 'PENDING_SHIP') {
      acts = `<button class="mini-btn warn" onclick="applyRefund('${o.id}')">申请退款</button>` + acts;
    } else if (o.status === 'CLOSED') {
      acts = `<button class="mini-btn" onclick="latePay('${o.id}')">模拟迟到支付</button>` + acts;
    }
    return `<div class="card">
      <div class="row" style="padding:0 0 8px;display:flex;gap:8px;align-items:center">
        <img src="${esc(o.productImage)}" style="width:48px;height:48px;border-radius:8px;object-fit:cover"/>
        <div style="flex:1"><div style="font-size:13px">${esc(o.productName)}</div>
        <div style="color:var(--pdd-red);font-weight:700">¥${formatYuan(o.amountFen)} · ${o.qty}件</div></div>
        <span class="pill ${s.cls}">${s.label}</span></div>
      <div class="mono">${esc(o.id)} · ${fmtTime(o.createdAt)}</div>
      <div class="statusline">${s.side}</div>
      <div style="margin-top:8px">${acts}</div></div>`;
  }).join('');
  return `<div class="panel-wrap"><div class="sec-title">我的订单 <span class="tag">仅本人</span></div>
    ${rows || '<div class="empty">你还没有订单，去首页逛逛吧～</div>'}</div>`;
}
function payOrder(id) {
  (async () => {
    try {
      const intent = await api(`/api/orders/${id}/pay-intent`, { method: 'POST', body: {} });
      window._intent = intent;
      const o = db.orders.find((x) => x.id === id);
      mask(`<div class="sheet">
        <span class="close-x" onclick="closeMask()">×</span><h3>模拟支付渠道</h3>
        <div class="detail-row"><span class="k">订单金额</span><span class="v">¥${formatYuan(intent.amountFen)}</span></div>
        <div class="detail-row"><span class="k">渠道交易号</span><span class="v mono">${esc(intent.channelTxnId)}</span></div>
        <div class="detail-row"><span class="k">通知签名</span><span class="v mono">${esc(intent.signature.slice(0, 16))}…</span></div>
        <div class="note" style="margin-top:10px">渠道已受理付款并生成<b>带签名的支付通知</b>。投递后服务端会验签、匹配订单与金额，并按渠道交易号做幂等。</div>
        <button class="submit" onclick="deliverPay(false)">确认支付（投递渠道通知）</button>
        <button class="submit ghost" onclick="deliverPay(true)">重复投递同一通知（幂等演示）</button>
        <div class="subtle">当前订单：${esc(o ? o.id : id)}</div>
      </div>`);
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
    toast(`${repeat ? '重复投递' : '支付'}结果：${res.outcome}｜${res.detail}`, 3400);
    if (!repeat) closeMask();
    await refresh(); render();
  } catch (e) {
    toast('通知被拒绝：' + e.message, 3200);
    await refresh(); render();
  } finally {
    busy = false;
  }
}
function confirmAction(title, note, onConfirm) {
  mask(`<div class="sheet">
    <span class="close-x" onclick="closeMask()">×</span><h3>${esc(title)}</h3>
    <div class="note">${note}</div>
    <button class="submit" onclick="window._confirmRun()">确认</button>
    <button class="submit ghost" onclick="closeMask()">取消</button></div>`);
  window._confirmRun = async () => { closeMask(); await onConfirm(); };
}
function cancelOrder(id) {
  confirmAction('取消订单', '取消后订单进入<b>已关闭</b>并释放占用的库存；重复取消不会重复释放。',
    () => act(() => api(`/api/orders/${id}/cancel`, { method: 'POST', body: {} }), '已取消，库存释放一次'));
}
function expireOrder(id) {
  confirmAction('模拟超时关闭', '由<b>服务端时间</b>判定为超过 10 分钟未付款，订单关闭并释放占用一次。',
    () => act(() => api(`/api/orders/${id}/expire`, { method: 'POST', body: {} }), '服务端判定已超时，订单关闭并释放占用'));
}
function latePay(id) {
  act(async () => {
    const intent = await api(`/api/orders/${id}/pay-intent`, { method: 'POST', body: {} });
    const res = await api('/api/payments/notify', { method: 'POST', body: intent });
    toast(`迟到支付：${res.outcome}｜不复活订单、不重占库存`, 3400);
  });
}
function applyRefund(id) {
  mask(`<div class="sheet">
    <span class="close-x" onclick="closeMask()">×</span><h3>申请整单退款</h3>
    <div class="note">仅「待发货」订单可申请。提交后订单进入<b>退款中</b>，仓库发货会被立即挡住；客服拒绝后可继续发货。</div>
    <input id="refundReason" class="form-input" placeholder="填写退款原因（选填）"/>
    <button class="submit" onclick="submitApplyRefund('${id}')">提交申请</button>
  </div>`);
}
function submitApplyRefund(id) {
  const reason = ($('#refundReason') && $('#refundReason').value) || '';
  act(() => api(`/api/orders/${id}/refund-apply`, { method: 'POST', body: { reason } }), '已申请退款，发货动作被冻结');
}
function viewOrder(id) {
  const o = db.orders.find((x) => x.id === id);
  if (!o) { toast('订单不存在'); return; }
  const s = stOf(o.status);
  const row = (k, v) => `<div class="detail-row"><span class="k">${k}</span><span class="v">${v}</span></div>`;
  mask(`<div class="sheet">
    <span class="close-x" onclick="closeMask()">×</span><h3>订单详情</h3>
    ${row('状态', `<span class="pill ${s.cls}">${s.label}</span>`)}
    ${row('商品', esc(o.productName))}
    ${row('单价快照', '¥' + formatYuan(o.priceFen))}
    ${row('数量 / 合计', o.qty + ' 件 / ¥' + formatYuan(o.amountFen))}
    ${row('下单时间', fmtTime(o.createdAt))}
    ${row('付款截止', fmtTime(o.expiresAt))}
    ${row('渠道交易号', esc(o.channelTxnId || '-'))}
    ${row('退款单 / 发货记录', esc(o.refundId || '-') + ' / ' + esc(o.shipmentId || '-'))}
    ${row('订单号', `<span class="mono">${esc(o.id)}</span>`)}
    <div class="note" style="margin-top:10px">${s.side}</div>
    <button class="submit" onclick="closeMask()">关闭</button>
  </div>`);
}

/* ================= 运营 ================= */
function renderOps() {
  const s = db.summary || {};
  const prodRows = db.products.map((p) => `<tr>
    <td>${esc(p.name.slice(0, 10))}…</td><td>${p.total}</td>
    <td style="color:${p.available > 0 ? 'var(--ok)' : '#c0392b'};font-weight:600">${p.available}</td>
    <td><button class="mini-btn ${p.onSale ? 'warn' : 'primary'}" onclick="toggleSale('${p.id}',${!p.onSale})">${p.onSale ? '下架' : '上架'}</button></td>
  </tr>`).join('');
  const abnRows = db.abnormal.map((a) => `<tr>
    <td class="mono">${esc(a.id)}</td><td>¥${formatYuan(a.amountFen)}</td>
    <td><span class="pill ${a.status === 'PENDING' ? 'abnormal' : 'refunded'}">${a.status === 'PENDING' ? '待处理' : '已退款'}</span></td>
    <td class="mono">${esc(a.orderId)}</td></tr>`).join('') || '<tr><td colspan="4" style="color:#bbb">暂无异常支付</td></tr>';
  const notifyRows = db.notifyResults.slice(-8).reverse().map((n) => `<tr>
    <td>${esc(n.outcome)}</td><td>¥${formatYuan(n.amountFen)}</td>
    <td class="mono">${esc(n.orderId)}</td><td style="font-size:11px;color:#999">${esc(n.detail)}</td></tr>`).join('') ||
    '<tr><td colspan="4" style="color:#bbb">暂无支付通知</td></tr>';
  const ledgerRows = db.ledger.slice(-12).reverse().map((l) => `<tr>
    <td>${esc(l.type)}</td><td>¥${formatYuan(l.amountFen)}</td>
    <td class="mono">${esc(l.orderId || '-')}</td><td style="font-size:11px;color:#999">${esc(l.note)}</td></tr>`).join('');
  return `<div class="panel-wrap">
    <div class="sec-title">库存核查 <span class="tag">运营</span></div>
    <div class="card"><table class="t"><tr><th>商品</th><th>总量</th><th>可售</th><th>操作</th></tr>${prodRows}</table></div>
    <div class="sec-title">对账汇总 <span class="tag">运营</span></div>
    <div class="stat-row">
      <div class="stat"><div class="n">¥${formatYuan(s.receivedFen)}</div><div class="l">收款总额</div></div>
      <div class="stat"><div class="n">¥${formatYuan(s.refundedFen)}</div><div class="l">退款总额</div></div>
      <div class="stat"><div class="n">¥${formatYuan(s.netFen)}</div><div class="l">净额</div></div>
      <div class="stat"><div class="n">${s.shippedCount || 0}</div><div class="l">已发货</div></div>
      <div class="stat"><div class="n">${s.abnormalCount || 0}</div><div class="l">异常支付</div></div>
      <div class="stat"><div class="n">${s.orderCount || 0}</div><div class="l">订单总数</div></div>
    </div>
    <div class="sec-title" style="margin-top:14px">异常支付记录 <span class="tag">不可抹除</span></div>
    <div class="card table-scroll"><table class="t"><tr><th>记录</th><th>金额</th><th>状态</th><th>订单</th></tr>${abnRows}</table></div>
    <div class="sec-title" style="margin-top:14px">支付通知处理结果 <span class="tag">R3 可查询</span></div>
    <div class="card table-scroll"><table class="t"><tr><th>结果</th><th>金额</th><th>订单</th><th>说明</th></tr>${notifyRows}</table></div>
    <div class="sec-title" style="margin-top:14px">流水（只增不改） <span class="tag">R8</span></div>
    <div class="card table-scroll"><table class="t"><tr><th>类型</th><th>金额</th><th>订单</th><th>说明</th></tr>${ledgerRows}</table></div>
    <div class="note">共 ${s.ledgerCount || db.ledger.length} 条流水。运营只能核查与维护商品上下架，<b>不能通过修改订单状态让异常记录消失</b>。</div>
    <button class="mini-btn warn" onclick="resetDemo()">重置演示数据</button>
  </div>`;
}
function toggleSale(pid, onSale) {
  act(() => api(`/api/products/${pid}/onsale`, { method: 'POST', body: { onSale } }), onSale ? '商品已上架' : '商品已下架');
}
function resetDemo() {
  mask(`<div class="sheet"><span class="close-x" onclick="closeMask()">×</span><h3>重置演示数据</h3>
    <div class="note">恢复为预置的 2 个商品、1 条待发货订单与 1 条收款流水，用于重新开始五个验收场景。</div>
    <button class="submit" onclick="act(()=>api('/api/reset',{method:'POST',body:{}}),'演示数据已重置')">确认重置</button></div>`);
}

/* ================= 仓库 ================= */
function renderWarehouse() {
  const rows = db.orders.map((o) => `<div class="card">
    <div style="display:flex;gap:8px;align-items:center;padding-bottom:8px">
      <img src="${esc(o.productImage)}" style="width:48px;height:48px;border-radius:8px;object-fit:cover"/>
      <div style="flex:1"><div style="font-size:13px">${esc(o.productName)}</div>
      <div style="font-size:12px;color:#999">${esc(o.buyerName)} · ${o.qty}件 · ¥${formatYuan(o.amountFen)}</div></div>
      <span class="pill ship">待发货</span></div>
    <div class="mono" style="margin:4px 0 8px">${esc(o.id)}</div>
    <button class="mini-btn primary" onclick="ship('${o.id}')">登记发货（一次）</button>
    <button class="mini-btn" onclick="viewOrder('${o.id}')">查看详情</button></div>`).join('') || '<div class="empty">暂无待发货订单</div>';
  return `<div class="panel-wrap">
    <div class="sec-title">待发货清单 <span class="tag">仓库</span></div>
    <div class="note">仅展示「待发货」订单；<b>退款中订单不会出现在本页</b>（申请退款期间暂停发货）。发货记录对订单唯一，重复登记会被后端拒绝。</div>
    ${rows}
    <div class="sec-title" style="margin-top:14px">已登记发货 <span class="tag">${db.shipments.length}</span></div>
    <div class="card table-scroll"><table class="t"><tr><th>订单</th><th>承运方</th><th>运单号</th></tr>
      ${db.shipments.map((sh) => `<tr><td class="mono">${esc(sh.orderId)}</td><td>${esc(sh.carrier)}</td><td class="mono">${esc(sh.trackingNo)}</td></tr>`).join('') || '<tr><td colspan="3" style="color:#bbb">暂无发货记录</td></tr>'}
    </table></div>
  </div>`;
}
function ship(id) {
  act(() => api(`/api/orders/${id}/ship`, { method: 'POST', body: { carrier: '顺丰速运', trackingNo: '' } }), '发货登记成功（仅一次）');
}

/* ================= 客服 ================= */
function renderCs() {
  const refundRows = db.refunds.map((r) => {
    const o = db.orders.find((x) => x.id === r.orderId) || {};
    const rst = REFUND_STATUS[r.status] || { label: r.status, cls: 'closed' };
    let acts = '';
    if (r.status === 'PENDING') {
      acts = `<button class="mini-btn ok" onclick="reviewRefund('${r.id}',true)">同意退款</button>
              <button class="mini-btn warn" onclick="reviewRefund('${r.id}',false)">拒绝并填写原因</button>`;
    } else if (r.status === 'AWAITING') {
      acts = `<button class="mini-btn ok" onclick="channelRefund('${r.id}',true)">渠道确认成功</button>
              <button class="mini-btn warn" onclick="channelRefund('${r.id}',false)">渠道返回失败</button>`;
    } else if (r.status === 'FAILED') {
      acts = `<button class="mini-btn primary" onclick="retryRefund('${r.id}')">重试退款</button>`;
    } else {
      acts = `<span class="subtle">无需操作</span>`;
    }
    return `<div class="card">
      <div style="display:flex;justify-content:space-between;gap:8px">
        <div style="font-size:13px">${esc(o.productName || r.orderId)}</div>
        <span class="pill ${rst.cls}">${rst.label}</span></div>
      <div class="mono" style="margin:4px 0">${esc(r.id)} · ${esc(r.orderId)}</div>
      <div class="statusline">买家 ${esc(o.buyerName || '-')} · 金额 ¥${formatYuan(r.amountFen)} · 来源 <b>${r.source === 'late' ? '迟到支付（不恢复库存）' : '售前退款（恢复库存）'}</b> · 第 ${r.attempts} 次发起</div>
      <div class="statusline">原因：${esc(r.reason || '-')}${r.reviewNote ? ' · 审核备注：' + esc(r.reviewNote) : ''}</div>
      <div style="margin-top:8px">${acts}</div></div>`;
  }).join('') || '<div class="empty">暂无退款单</div>';
  const abnRows = db.abnormal.map((a) => {
    const o = db.orders.find((x) => x.id === a.orderId) || {};
    return `<div class="card">
      <div style="display:flex;justify-content:space-between;gap:8px">
        <div style="font-size:13px">${esc(o.productName || a.orderId)} · ${esc(o.buyerName || '-')}</div>
        <span class="pill ${a.status === 'PENDING' ? 'abnormal' : 'refunded'}">${a.status === 'PENDING' ? '待处理' : '已退款'}</span></div>
      <div class="mono" style="margin:4px 0">${esc(a.id)} · ${esc(a.orderId)}</div>
      <div class="statusline">¥${formatYuan(a.amountFen)} · ${esc(a.reason)}</div>
      ${a.status === 'PENDING'
        ? `<div style="margin-top:8px"><button class="mini-btn primary" onclick="handleAbnormal('${a.id}')">核实并退款（不恢复库存）</button></div>`
        : `<div class="statusline">${esc(a.handleNote || '')} · ${fmtTime(a.handledAt)}</div>`}
    </div>`;
  }).join('') || '<div class="empty">暂无异常支付</div>';
  return `<div class="panel-wrap">
    <div class="sec-title">退款审核 <span class="tag">客服</span></div>
    <div class="note">同意后进入「等待渠道确认」；<b>只有渠道确认成功才算退款成功</b>，失败保留待处理可重试。售前退款恢复库存，迟到支付退款不恢复。</div>
    ${refundRows}
    <div class="sec-title" style="margin-top:14px">异常支付处理 <span class="tag">客服</span></div>
    <div class="note">订单关闭后到达的成功支付通知，<b>不复活订单、不重占库存</b>，在此人工核实退款；关闭时库存已释放过一次，退款不再恢复。</div>
    ${abnRows}
  </div>`;
}
function reviewRefund(rid, approve) {
  if (approve) {
    act(() => api(`/api/refunds/${rid}/review`, { method: 'POST', body: { approve: true, note: '核实无误，同意退款' } }), '已同意，等待渠道确认退款结果');
    return;
  }
  mask(`<div class="sheet"><span class="close-x" onclick="closeMask()">×</span><h3>拒绝退款申请</h3>
    <div class="note">拒绝后订单退回「待发货」，仓库可继续发货；请写明拒绝原因。</div>
    <input id="reviewNote" class="form-input" placeholder="填写拒绝原因"/>
    <button class="submit" onclick="submitReview('${rid}')">提交拒绝</button></div>`);
}
function submitReview(rid) {
  const note = ($('#reviewNote') && $('#reviewNote').value) || '';
  if (!note.trim()) { toast('请填写拒绝原因'); return; }
  act(() => api(`/api/refunds/${rid}/review`, { method: 'POST', body: { approve: false, note } }), '已拒绝，订单恢复待发货');
}
function channelRefund(rid, success) {
  act(() => api(`/api/refunds/${rid}/channel-notify`, { method: 'POST', body: { success } }),
    success ? '渠道确认退款成功' : '渠道退款失败，退款单保留待处理可重试');
}
function retryRefund(rid) {
  act(() => api(`/api/refunds/${rid}/retry`, { method: 'POST', body: {} }), '已重新向渠道发起退款');
}
function handleAbnormal(aid) {
  act(() => api(`/api/abnormal/${aid}/handle`, { method: 'POST', body: {} }),
    '已发起迟到支付退款，请在退款审核区完成渠道确认');
}

/* ================= 越权演示（真实调用后端接口） ================= */
// skipGuard：故意绕过前端守卫，真实调用后端接口以取得 403 硬证据
const FORBIDDEN_CALLS = {
  '商品上下架': () => api('/api/products/P1/onsale', { method: 'POST', body: { onSale: false }, skipGuard: true }),
  '登记发货': () => api('/api/orders/NO20260001/ship', { method: 'POST', body: {}, skipGuard: true }),
  '审核退款': () => api('/api/refunds/RF-DEMO/review', { method: 'POST', body: { approve: true, note: '越权演示' }, skipGuard: true }),
  '处理异常支付': () => api('/api/abnormal/ABN-DEMO/handle', { method: 'POST', body: {}, skipGuard: true }),
  '下单/支付': () => api('/api/orders', { method: 'POST', body: { productId: 'P1', qty: 1 }, skipGuard: true }),
};
function renderGuard() {
  const forbidden = {
    buyer: ['商品上下架', '登记发货', '审核退款', '处理异常支付'],
    ops: ['下单/支付', '登记发货', '审核退款'],
    warehouse: ['下单/支付', '商品上下架', '审核退款', '处理异常支付'],
    cs: ['下单/支付', '商品上下架', '登记发货'],
  }[current.role] || [];
  const btns = forbidden.map((a) => `<button class="mini-btn warn" onclick="tryForbidden('${a}')">尝试「${a}」</button>`).join('');
  return `<div class="guard"><h4>🔒 越权演示（当前：${ROLES[current.role].name}）</h4>
    <p>点击下方按钮，会以当前身份<b>真实调用</b>其他角色的后端接口。权限守卫在业务层统一拦截，返回 403 且不做任何数据改动。</p>
    <div class="forbidden">${btns}</div></div>`;
}
async function tryForbidden(action) {
  const fn = FORBIDDEN_CALLS[action];
  let status = 0, msg = '';
  try {
    await fn();
    msg = '请求未被拦截（异常情况，请检查权限守卫）';
  } catch (e) { status = e.status; msg = e.message; }
  mask(`<div class="sheet"><div class="deny">
    <div class="ic">⛔</div><div class="t1">无权访问（越权拦截）</div>
    <div class="t2">当前身份：<b>${ROLES[current.role].name} / ${esc(current.name)}</b><br/>
    操作「${esc(action)}」已被后端拒绝（HTTP ${status || '-'}）。<br/>
    ${esc(msg)}<br/><b>系统未做任何数据改动。</b></div>
    <button class="submit" onclick="closeMask()">我知道了</button></div></div>`);
}

/* ================= 启动 ================= */
(async function init() {
  try { meta = await api('/api/meta'); } catch (e) { meta = null; }
  renderLogin();
})();

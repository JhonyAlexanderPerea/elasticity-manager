package api

const dashboardHTML = `<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="UTF-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1"/>
<title>Elastic LB Manager</title>
<style>
:root{--bg:#0b0f1c;--surface:#111827;--s2:#1a2235;--border:#1e2d45;--accent:#00d4ff;--green:#00ff88;--red:#ff4d6d;--yellow:#ffb830;--text:#e2e8f0;--muted:#64748b;--mono:'JetBrains Mono',monospace;--r:8px;--t:.18s ease}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font-family:'Inter',system-ui,sans-serif;min-height:100vh}
header{display:flex;align-items:center;justify-content:space-between;padding:13px 26px;background:var(--surface);border-bottom:1px solid var(--border);position:sticky;top:0;z-index:99}
.logo{display:flex;align-items:center;gap:9px;font-weight:700;font-size:1rem}
.dot{width:9px;height:9px;border-radius:50%;background:var(--accent);box-shadow:0 0 8px var(--accent);animation:pulse 2s infinite}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.3}}
.hright{display:flex;align-items:center;gap:10px}
.badge{padding:3px 9px;border-radius:20px;font-size:.72rem;font-weight:700;text-transform:uppercase;letter-spacing:.4px}
.bon{background:#012820;color:var(--green);border:1px solid #065f46}
.boff{background:#2a0a0a;color:var(--red);border:1px solid #7f1d1d}
#stime{font-size:.78rem;color:var(--muted);font-family:var(--mono)}
.app{display:grid;grid-template-columns:210px 1fr;min-height:calc(100vh - 50px)}
nav{background:var(--surface);border-right:1px solid var(--border);padding:16px 0}
nav a{display:flex;align-items:center;gap:9px;padding:9px 18px;color:var(--muted);font-size:.875rem;cursor:pointer;transition:var(--t);border-left:3px solid transparent;text-decoration:none}
nav a:hover,nav a.active{color:var(--text);background:var(--s2);border-left-color:var(--accent)}
.slabel{padding:14px 18px 5px;font-size:.68rem;color:var(--muted);text-transform:uppercase;letter-spacing:.9px}
main{padding:26px;overflow-y:auto}
.page{display:none}.page.active{display:block}
.g4{display:grid;grid-template-columns:repeat(4,1fr);gap:14px;margin-bottom:20px}
.g2{display:grid;grid-template-columns:1fr 1fr;gap:18px}
.card{background:var(--surface);border:1px solid var(--border);border-radius:var(--r);padding:18px}
.sl{font-size:.72rem;color:var(--muted);text-transform:uppercase;letter-spacing:.4px;margin-bottom:5px}
.sv{font-size:1.9rem;font-weight:700;font-family:var(--mono)}
.ss{font-size:.72rem;color:var(--muted);margin-top:3px}
.ca{color:var(--accent)}.cg{color:var(--green)}.cr{color:var(--red)}.cy{color:var(--yellow)}
.ph{display:flex;align-items:center;justify-content:space-between;margin-bottom:18px}
.pt{font-size:1.2rem;font-weight:700}.ps{font-size:.82rem;color:var(--muted);margin-top:2px}
.btn{display:inline-flex;align-items:center;gap:5px;padding:7px 14px;border-radius:6px;border:none;font-size:.82rem;font-weight:600;cursor:pointer;transition:var(--t)}
.bp{background:var(--accent);color:#000}.bp:hover{background:#00bfff}
.bs{background:var(--green);color:#000}.bs:hover{opacity:.85}
.bd{background:var(--red);color:#fff}.bd:hover{opacity:.85}
.bg{background:var(--s2);color:var(--text);border:1px solid var(--border)}.bg:hover{border-color:var(--accent);color:var(--accent)}
.bsm{padding:4px 9px;font-size:.73rem}
.bgrp{display:flex;gap:7px;flex-wrap:wrap}
table{width:100%;border-collapse:collapse}
th{text-align:left;padding:9px 12px;font-size:.7rem;color:var(--muted);text-transform:uppercase;letter-spacing:.4px;border-bottom:1px solid var(--border)}
td{padding:10px 12px;font-size:.83rem;border-bottom:1px solid var(--border)}
tr:last-child td{border-bottom:none}
tr:hover td{background:var(--s2)}
.tag{display:inline-block;padding:2px 7px;border-radius:4px;font-size:.68rem;font-weight:700}
.tg{background:#012820;color:var(--green)}.tb{background:#0c1a2e;color:var(--accent)}.tr{background:#2a0a0a;color:var(--red)}.ty{background:#2a1800;color:var(--yellow)}
.fg{margin-bottom:12px}
label{display:block;font-size:.78rem;color:var(--muted);margin-bottom:4px;font-weight:500}
input,select{width:100%;padding:7px 10px;background:var(--s2);border:1px solid var(--border);border-radius:6px;color:var(--text);font-size:.85rem;transition:var(--t)}
input:focus,select:focus{outline:none;border-color:var(--accent);box-shadow:0 0 0 2px rgba(0,212,255,.12)}
.fr{display:grid;grid-template-columns:1fr 1fr;gap:10px}
.rg{display:flex;align-items:center;gap:8px}
input[type=range]{flex:1;accent-color:var(--accent)}
.rv{font-family:var(--mono);font-size:.82rem;min-width:38px;text-align:right}
.overlay{display:none;position:fixed;inset:0;background:rgba(0,0,0,.55);z-index:200;align-items:center;justify-content:center}
.overlay.open{display:flex}
.modal{background:var(--surface);border:1px solid var(--border);border-radius:12px;padding:24px;width:460px;max-width:95vw;max-height:90vh;overflow-y:auto}
.mtitle{font-size:1.05rem;font-weight:700;margin-bottom:18px;display:flex;justify-content:space-between;align-items:center}
.mclose{cursor:pointer;background:none;border:none;color:var(--text);font-size:1.1rem}
.log{background:var(--s2);border:1px solid var(--border);border-radius:var(--r);padding:12px;height:320px;overflow-y:auto;font-family:var(--mono);font-size:.76rem;line-height:1.8}
.terminal{background:#090d17;border:1px solid var(--border);border-radius:var(--r);padding:12px;height:280px;overflow-y:auto;font-family:var(--mono);font-size:.75rem;line-height:1.6;color:#c5d1e6;white-space:pre-wrap}
.termline{margin-bottom:2px}
.tprompt{color:var(--accent);margin-right:7px}
.le{margin-bottom:1px}
.li{color:var(--accent)}.lso{color:var(--green)}.lsi{color:var(--yellow)}.lw{color:var(--red)}
.lt{color:var(--muted);margin-right:7px}
.chart-area{width:100%;height:150px;position:relative}
canvas{width:100%!important;height:150px!important}
.empty{text-align:center;color:var(--muted);padding:36px;font-size:.88rem}
.alert{padding:9px 13px;border-radius:6px;font-size:.82rem;margin-bottom:12px}
.ae{background:#2a0a0a;border:1px solid #7f1d1d;color:var(--red)}
.as{background:#012820;border:1px solid #065f46;color:var(--green)}
.st{font-size:.95rem;font-weight:700;margin-bottom:12px;padding-bottom:9px;border-bottom:1px solid var(--border)}
pre{background:var(--s2);border:1px solid var(--border);border-radius:var(--r);padding:13px;font-size:.73rem;font-family:var(--mono);overflow-x:auto;white-space:pre-wrap;max-height:380px;overflow-y:auto}
.infobox{background:var(--s2);border-left:3px solid var(--accent);padding:10px 13px;border-radius:4px;font-size:.82rem;line-height:1.7;color:var(--muted);margin-bottom:14px}
.infobox strong{color:var(--text)}
.uptb{display:flex;align-items:center;gap:7px;font-size:.78rem;color:var(--muted)}
.updot{width:7px;height:7px;border-radius:50%;background:var(--green);flex-shrink:0}
.gw{position:relative;width:110px;height:110px;margin:0 auto 10px}
.gsvg{transform:rotate(-90deg)}
.gtrack{fill:none;stroke:var(--border);stroke-width:9}
.gfill{fill:none;stroke-width:9;stroke-linecap:round;transition:stroke-dashoffset .5s ease,stroke .25s}
.gitem{opacity:1;transform:translateY(0) scale(1);transition:opacity .32s ease,transform .32s ease,filter .32s ease;will-change:opacity,transform}
.gitem.entering{opacity:0;transform:translateY(8px) scale(.96)}
.gitem.leaving{opacity:0;transform:translateY(-8px) scale(.95);filter:saturate(.85)}
.glabel{position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;font-family:var(--mono);font-size:1rem;font-weight:700}
.gsub{font-size:.62rem;color:var(--muted);margin-top:1px}
@media(max-width:1100px){.g4{grid-template-columns:1fr 1fr}.g2{grid-template-columns:1fr}}
@media(max-width:760px){.app{grid-template-columns:1fr}nav{display:flex;overflow-x:auto;padding:0}nav a{flex-direction:column;padding:9px 14px;font-size:.68rem;gap:3px;border-left:none;border-bottom:3px solid transparent;white-space:nowrap}nav a.active{border-bottom-color:var(--accent);border-left-color:transparent}.slabel{display:none}.g4{grid-template-columns:1fr 1fr}.fr{grid-template-columns:1fr}.g2{grid-template-columns:1fr}}
</style>
</head>
<body>
<header>
  <div class="logo"><div class="dot"></div>Elastic LB Manager<span style="color:var(--muted);font-size:.73rem;font-weight:400;margin-left:4px">Univ. Quindío</span></div>
  <div class="hright"><span id="sbadge" class="badge bon">Auto-scale ON</span><span id="stime">—</span></div>
</header>
<div class="app">
<nav>
  <div class="slabel">Panel</div>
  <a class="active" onclick="nav('dashboard',this)">
    <svg width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/></svg>Dashboard</a>
  <div class="slabel">Balanceadores</div>
  <a onclick="nav('backends',this)">
    <svg width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14c0 1.66 4.03 3 9 3s9-1.34 9-3V5"/><path d="M3 12c0 1.66 4.03 3 9 3s9-1.34 9-3"/></svg>Backends</a>
  <div class="slabel">Infraestructura</div>
  <a onclick="nav('vms',this)">
    <svg width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8m-4-4v4"/></svg>Máq. Virtuales</a>
  <div class="slabel">Elasticidad</div>
  <a onclick="nav('config',this)">
    <svg width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14M4.93 4.93a10 10 0 0 0 0 14.14"/></svg>Auto-Scaler</a>
  <a onclick="nav('simulate',this)">
    <svg width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>Simulación</a>
  <div class="slabel">HAProxy</div>
  <a onclick="nav('haproxy',this)">
    <svg width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>Config Raw</a>
</nav>
<main>

<!-- DASHBOARD -->
<div id="page-dashboard" class="page active">
  <div class="ph"><div><div class="pt">Dashboard</div><div class="ps">Estado en tiempo real</div></div>
  <div class="uptb"><div class="updot"></div><span id="uptime">—</span></div></div>
  <div class="g4">
    <div class="card"><div class="sl">Instancias Activas</div><div class="sv ca" id="si">0</div><div class="ss">máquinas virtuales</div></div>
    <div class="card"><div class="sl">CPU Promedio</div><div class="sv cg" id="sc">0%</div><div class="ss">ventana de evaluación</div></div>
    <div class="card"><div class="sl">Backends</div><div class="sv ca" id="sb">0</div><div class="ss">balanceadores activos</div></div>
    <div class="card"><div class="sl">Auto-Scaler</div><div class="sv" id="ss2" style="font-size:1.1rem">—</div><div class="ss" id="ss3">—</div></div>
  </div>
  <div class="g2">
    <div class="card"><div class="st">CPU por Instancia</div><div id="gauges" style="display:flex;flex-wrap:wrap;gap:18px;justify-content:center"><div class="empty">Sin instancias</div></div></div>
    <div class="card"><div class="st">Historial CPU</div><div class="chart-area"><canvas id="cc"></canvas></div></div>
  </div>
  <div class="card" style="margin-top:18px">
    <div class="ph"><div><div class="st" style="margin-bottom:0;border-bottom:none;padding-bottom:0">Terminal de Logs</div><div class="ps">Salida reciente de eventos del sistema</div></div>
    <button class="btn bg bsm" onclick="loadTerminalLogs()">↻ Actualizar</button></div>
    <div id="tlog" class="terminal">Cargando...</div>
  </div>
  <div class="card" style="margin-top:18px"><div class="st">Parámetros actuales</div>
    <div class="g4">
      <div><div class="sl">Umbral Superior</div><div class="sv cr" id="cu" style="font-size:1.2rem">—</div></div>
      <div><div class="sl">Umbral Inferior</div><div class="sv cg" id="cl" style="font-size:1.2rem">—</div></div>
      <div><div class="sl">Intervalo</div><div class="sv ca" id="ci" style="font-size:1.2rem">—</div></div>
      <div><div class="sl">Ventana</div><div class="sv ca" id="cw" style="font-size:1.2rem">—</div></div>
    </div>
  </div>
</div>

<!-- BACKENDS -->
<div id="page-backends" class="page">
  <div class="ph"><div><div class="pt">Backends HAProxy</div><div class="ps">Gestión de balanceadores</div></div>
  <button class="btn bp" onclick="openModal('mb')">+ Nuevo Backend</button></div>
  <div id="ab"></div><div id="bl"></div>
</div>

<!-- VMs -->
<div id="page-vms" class="page">
  <div class="ph"><div><div class="pt">Máquinas Virtuales</div><div class="ps">Instancias gestionadas</div></div>
  <button class="btn bg bsm" onclick="loadVMs()">↻ Actualizar</button></div>
  <div class="infobox">En Windows, las VMs corren en <strong>VirtualBox</strong>. El acceso SSH se realiza via NAT port-forwarding desde <code>127.0.0.1:22XX</code> a cada VM.</div>
  <div id="vt"></div>
</div>

<!-- CONFIG -->
<div id="page-config" class="page">
  <div class="ph"><div><div class="pt">Auto-Scaler</div><div class="ps">Umbrales e intervalos</div></div>
  <div class="bgrp"><button class="btn bs bsm" onclick="scalerToggle(true)">▶ Activar</button><button class="btn bd bsm" onclick="scalerToggle(false)">■ Pausar</button></div></div>
  <div id="ac"></div>
  <div class="g2">
    <div class="card">
      <div class="st">Parámetros</div>
      <div class="fg"><label>Umbral Superior (%) — escalar hacia arriba</label>
        <div class="rg"><input type="range" id="ut" min="30" max="99" value="80" oninput="document.getElementById('uv').textContent=this.value+'%'"><span class="rv" id="uv">80%</span></div></div>
      <div class="fg"><label>Umbral Inferior (%) — escalar hacia abajo</label>
        <div class="rg"><input type="range" id="lt" min="1" max="60" value="20" oninput="document.getElementById('lv').textContent=this.value+'%'"><span class="rv" id="lv">20%</span></div></div>
      <div class="fg"><label>Umbral Pico (%) — escalar por hotspot</label>
        <div class="rg"><input type="range" id="pt" min="50" max="100" value="90" oninput="document.getElementById('pv').textContent=this.value+'%'"><span class="rv" id="pv">90%</span></div></div>
      <div class="fr">
        <div class="fg"><label>Intervalo muestreo (seg)</label><input type="number" id="iv" min="5" max="300" value="10"></div>
        <div class="fg"><label>Ventana evaluación (seg)</label><input type="number" id="ev" min="10" max="600" value="60"></div>
      </div>
      <div class="fr">
        <div class="fg"><label>Máx instancias</label><input type="number" id="mx" min="2" max="20" value="5"></div>
        <div class="fg"><label>Mín instancias</label><input type="number" id="mn" min="2" max="10" value="2"></div>
      </div>
      <button class="btn bp" style="width:100%" onclick="saveConfig()">Guardar</button>
    </div>
    <div class="card">
      <div class="st">Lógica de escala</div>
      <div style="font-size:.83rem;line-height:1.8;color:var(--muted);display:flex;flex-direction:column;gap:10px">
        <div style="background:var(--s2);border-left:3px solid var(--red);padding:9px 12px;border-radius:4px"><strong style="color:var(--red)">Scale-Out</strong><br>CPU prom &gt; Umbral Superior → clonar VM base → iniciar headless → registrar en HAProxy</div>
        <div style="background:var(--s2);border-left:3px solid var(--green);padding:9px 12px;border-radius:4px"><strong style="color:var(--green)">Scale-In</strong><br>CPU prom &lt; Umbral Inferior → quitar del HAProxy → apagar y conservar VM</div>
        <div style="background:var(--s2);border-left:3px solid var(--accent);padding:9px 12px;border-radius:4px"><strong style="color:var(--accent)">Windows → VMs Linux</strong><br>VBoxManage.exe crea clones enlazados. HAProxy corre en <em>haproxy-vm</em> y se recarga via SSH.</div>
      </div>
    </div>
  </div>
</div>

<!-- SIMULATE -->
<div id="page-simulate" class="page">
  <div class="ph"><div><div class="pt">Simulación de Carga</div><div class="ps">stress-ng global / wrk vía HAProxy</div></div></div>
  <div id="as2"></div>
  <div class="g2">
    <div class="card">
      <div class="st">stress-ng vía SSH (global)</div>
      <p style="font-size:.81rem;color:var(--muted);margin-bottom:12px">Ejecuta <code style="background:var(--s2);padding:1px 5px;border-radius:3px">stress-ng</code> en todas las app-vm running para pruebas coordinadas.</p>
      <div class="fg"><label>CPU % objetivo</label><div class="rg"><input type="range" id="ssp" min="10" max="100" step="10" value="80" oninput="document.getElementById('ssv').textContent=this.value+'%'"><span class="rv" id="ssv">80%</span></div></div>
      <div class="fg"><label>Duración (seg)</label><input type="number" id="ssd" value="60" min="10" max="600"></div>
      <div class="bgrp" style="width:100%">
        <button class="btn bd" style="flex:1" onclick="runSim(true)">Ejecutar stress-ng</button>
        <button class="btn bg" style="flex:1" onclick="cancelSim(true)">Cancelar</button>
      </div>
    </div>
    <div class="card">
      <div class="st">wrk vía HAProxy SSH (balanceo real)</div>
      <p style="font-size:.81rem;color:var(--muted);margin-bottom:12px">Genera carga desde la VM de HAProxy y valida reparto real entre app-vm. Esta prueba no permite bypass directo a una VM.</p>
      <div class="fr">
        <div class="fg"><label>Threads (-t)</label><input type="number" id="wt" value="4" min="1" max="64"></div>
        <div class="fg"><label>Conexiones (-c)</label><input type="number" id="wc" value="100" min="1" max="2000"></div>
      </div>
      <div class="fr">
        <div class="fg"><label>Duración (seg)</label><input type="number" id="wd" value="30" min="1" max="86400"></div>
        <div class="fg"><label>Ruta objetivo</label><input type="text" id="wp" value="/heavy?ms=1200" placeholder="/heavy?ms=1200"></div>
      </div>
      <div class="bgrp" style="width:100%">
        <button class="btn bp" style="flex:1" onclick="runWrk()">Ejecutar wrk</button>
        <button class="btn bg" style="flex:1" onclick="cancelWrk()">Cancelar</button>
      </div>
    </div>
  </div>
</div>

<!-- HAPROXY -->
<div id="page-haproxy" class="page">
  <div class="ph"><div><div class="pt">Config HAProxy (generada)</div></div>
  <button class="btn bg bsm" onclick="loadHAP()">↻ Actualizar</button></div>
  <div class="card"><pre id="hraw">Cargando…</pre></div>
</div>

</main></div>

<!-- Modals -->
<div class="overlay" id="mb">
  <div class="modal">
    <div class="mtitle"><span id="bmt">Nuevo Backend</span><button class="mclose" onclick="closeModal('mb')">✕</button></div>
    <div id="amb"></div>
    <div class="fg"><label>Nombre</label><input type="text" id="bn" placeholder="app-backend" maxlength="64" pattern="[A-Za-z0-9][A-Za-z0-9_-]{1,63}"></div>
    <div class="fg"><label>Algoritmo</label><select id="ba"><option value="roundrobin">roundrobin</option><option value="leastconn">leastconn</option><option value="first">first</option></select></div>
    <div class="bgrp" style="margin-top:8px"><button class="btn bp" onclick="saveBackend()">Guardar</button><button class="btn bg" onclick="closeModal('mb')">Cancelar</button></div>
  </div>
</div>
<div class="overlay" id="ms">
  <div class="modal">
    <div class="mtitle"><span id="smt">Agregar Servidor</span><button class="mclose" onclick="closeModal('ms')">✕</button></div>
    <div id="ams"></div>
    <input type="hidden" id="sbk"><input type="hidden" id="son">
    <div class="fg"><label>Nombre</label><input type="text" id="snm" placeholder="app-vm-1" maxlength="64" pattern="[A-Za-z0-9][A-Za-z0-9_-]{1,63}"></div>
    <div class="fr">
      <div class="fg"><label>IP</label><input type="text" id="sip" placeholder="10.0.2.10" inputmode="numeric"></div>
      <div class="fg"><label>Puerto</label><input type="number" id="spt" value="8000" min="1" max="65535"></div>
    </div>
    <div class="fg"><label>Peso</label><input type="number" id="swt" value="1" min="1" max="256"></div>
    <div class="bgrp" style="margin-top:8px"><button class="btn bp" onclick="saveServer()">Guardar</button><button class="btn bg" onclick="closeModal('ms')">Cancelar</button></div>
  </div>
</div>

<script>
let eb=null,es=null,cd={},ci=0;
const seenGaugeInstances=new Set();
const gaugeNodes=new Map();
let gaugesBootstrapped=false;
let uiTickRunning=false;
let serviceStartId='';
let terminalPolling=false;
let terminalSource=null;
let terminalStreamConnected=false;
const terminalSeenKeys=new Set();
const terminalKeyOrder=[];
const terminalLines=[];
const maxTerminalLines=120;
const maxTerminalSeen=800;
const colors=['#00d4ff','#00ff88','#ffb830','#ff4d6d','#a78bfa','#fb923c'];
const api=p=>fetch(p,{cache:'no-store'}).then(r=>r.json());
const post=(p,b,m='POST')=>fetch(p,{method:m,headers:{'Content-Type':'application/json'},body:JSON.stringify(b)}).then(r=>r.json());
function nav(id,el){document.querySelectorAll('.page').forEach(p=>p.classList.remove('active'));document.querySelectorAll('nav a').forEach(a=>a.classList.remove('active'));document.getElementById('page-'+id).classList.add('active');el.classList.add('active');if(id==='dashboard')loadTerminalLogs();if(id==='backends')loadBackends();if(id==='vms')loadVMs();if(id==='config')loadConfig();if(id==='haproxy')loadHAP();}
function openModal(id){document.getElementById(id).classList.add('open')}
function closeModal(id){document.getElementById(id).classList.remove('open')}
document.querySelectorAll('.overlay').forEach(o=>o.addEventListener('click',e=>{if(e.target===o)o.classList.remove('open')}));
function alert2(id,msg,t='error'){const el=document.getElementById(id);el.innerHTML='<div class="alert a'+(t==='error'?'e':'s')+'">'+msg+'</div>';setTimeout(()=>el.innerHTML='',4000);}
function ft(iso){return new Date(iso).toLocaleTimeString('es-CO')}
function toInt(id,min,max){const n=parseInt(document.getElementById(id).value,10);if(Number.isNaN(n))return null;return Math.min(max,Math.max(min,n));}
function toFloat(id,min,max){const n=parseFloat(document.getElementById(id).value);if(Number.isNaN(n))return null;return Math.min(max,Math.max(min,n));}
function validName(v){return /^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$/.test(v||'');}
function validIPv4(v){if(!/^(\d{1,3}\.){3}\d{1,3}$/.test(v||''))return false;return v.split('.').every(p=>{const n=Number(p);return n>=0&&n<=255;});}
function validPath(v){if(!v||!v.startsWith('/'))return false;return !/[ '\";$&|<>]/.test(v);}
function eventKey(e){return (e.timestamp||'')+'|'+(e.kind||'')+'|'+(e.message||'')}
function esc(s){return String(s??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));}
function renderTerminal(){const el=document.getElementById('tlog');if(!el)return;if(!terminalLines.length){el.innerHTML='<span style="color:var(--muted)">Sin logs disponibles.</span>';return;}el.innerHTML=terminalLines.join('');el.scrollTop=el.scrollHeight;}
function resetTerminal(message){terminalSeenKeys.clear();terminalKeyOrder.length=0;terminalLines.length=0;if(message){terminalLines.push('<div class="termline"><span class="tprompt">$</span> '+esc(message)+'</div>');}renderTerminal();}
function appendTerminalEvent(e){const key=eventKey(e);if(terminalSeenKeys.has(key))return;terminalSeenKeys.add(key);terminalKeyOrder.push(key);if(terminalKeyOrder.length>maxTerminalSeen){const old=terminalKeyOrder.shift();terminalSeenKeys.delete(old);}const line='<div class="termline"><span class="tprompt">$</span><span class="lt">'+ft(e.timestamp)+'</span> ['+esc(e.kind)+'] '+esc(e.message)+'</div>';terminalLines.push(line);if(terminalLines.length>maxTerminalLines)terminalLines.shift();}
function connectTerminalStream(){if(typeof EventSource==='undefined')return;if(terminalSource){terminalSource.close();terminalSource=null;}const src=new EventSource('/api/events/stream');terminalSource=src;src.addEventListener('log',ev=>{try{const item=JSON.parse(ev.data);appendTerminalEvent(item);renderTerminal();}catch(e){console.error(e);}});src.onopen=()=>{terminalStreamConnected=true;};src.onerror=()=>{terminalStreamConnected=false;loadTerminalLogs(false);};}
// chart
let cl=[],cds={};
function gc(n){if(cd[n])return cd[n];if(ci<colors.length){cd[n]=colors[ci++];return cd[n];}const idx=ci++;const hue=(idx*137.508)%360;const sat=70+(idx%3)*6;const light=52+((idx+1)%2)*4;cd[n]='hsl('+hue.toFixed(1)+' '+sat+'% '+light+'%)';return cd[n];}
function updateChart(cpu){const canvas=document.getElementById('cc');if(!canvas)return;const ctx=canvas.getContext('2d');const now=new Date().toLocaleTimeString('es-CO');if(cl.length>20)cl.shift();cl.push(now);const names=Object.keys(cpu||{});const active=new Set(names);Object.keys(cds).forEach(n=>{if(!active.has(n))delete cds[n];});names.forEach(n=>{if(!cds[n])cds[n]=[];while(cds[n].length<cl.length-1)cds[n].push(0);if(cds[n].length>=20)cds[n].shift();cds[n].push(cpu[n]||0);});const w=canvas.offsetWidth||500,h=150;canvas.width=w;canvas.height=h;ctx.clearRect(0,0,w,h);ctx.strokeStyle='rgba(30,45,69,.6)';ctx.lineWidth=1;for(let i=0;i<=4;i++){const y=i*(h/4);ctx.beginPath();ctx.moveTo(0,y);ctx.lineTo(w,y);ctx.stroke();}const pts=cl.length;if(pts<2)return;const xs=w/(pts-1);names.forEach(n=>{const data=cds[n]||[];ctx.strokeStyle=gc(n);ctx.lineWidth=2;ctx.beginPath();data.forEach((v,i)=>{const x=i*xs,y=h-(v/100)*h;i===0?ctx.moveTo(x,y):ctx.lineTo(x,y);});ctx.stroke();});}
function createGaugeNode(name,value,isNew){const r=42,circ=2*Math.PI*r,p=Math.min(Math.max(value,0),100),dash=circ-(p/100)*circ;const vmColor=gc(name);const node=document.createElement('div');node.className='gitem'+(isNew?' entering':'');node.style.textAlign='center';node.dataset.name=name;node._cpuValue=(value||0);node._cpuRAF=0;node.innerHTML='<div class="gw"><svg class="gsvg" width="110" height="110" viewBox="0 0 110 110"><circle class="gtrack" cx="55" cy="55" r="'+r+'"/><circle class="gfill" cx="55" cy="55" r="'+r+'" stroke="'+vmColor+'" stroke-dasharray="'+circ+'" stroke-dashoffset="'+dash+'"/></svg><div class="glabel"><span>'+(value||0).toFixed(1)+'%</span><span class="gsub">CPU</span></div></div><div style="font-size:.72rem;color:'+vmColor+';margin-top:3px">'+name+'</div>';if(isNew){requestAnimationFrame(()=>{if(node.isConnected){node.classList.remove('entering');}});}return node;}
function updateGaugeNode(node,value){const target=Math.min(Math.max(value||0,0),100);const vmColor=gc(node.dataset.name||'vm');const circle=node.querySelector('.gfill');if(circle){const circ=parseFloat(circle.getAttribute('stroke-dasharray'))||2*Math.PI*42;circle.setAttribute('stroke',vmColor);circle.setAttribute('stroke-dashoffset',String(circ-(target/100)*circ));}const txt=node.querySelector('.glabel span');if(!txt)return;const from=(typeof node._cpuValue==='number'?node._cpuValue:target);if(Math.abs(target-from)<0.05){txt.textContent=target.toFixed(1)+'%';node._cpuValue=target;return;}if(node._cpuRAF){cancelAnimationFrame(node._cpuRAF);}const start=performance.now(),duration=260;const step=(now)=>{const t=Math.min(1,(now-start)/duration);const eased=1-Math.pow(1-t,3);const v=from+((target-from)*eased);txt.textContent=v.toFixed(1)+'%';node._cpuValue=v;if(t<1){node._cpuRAF=requestAnimationFrame(step);}else{node._cpuRAF=0;node._cpuValue=target;txt.textContent=target.toFixed(1)+'%';}};node._cpuRAF=requestAnimationFrame(step);}
function sortGaugeNames(names){return names.slice().sort((a,b)=>{const ma=a.match(/^app-vm-(\d+)$/),mb=b.match(/^app-vm-(\d+)$/);if(ma&&mb){return parseInt(ma[1],10)-parseInt(mb[1],10);}if(ma&&!mb){return -1;}if(!ma&&mb){return 1;}return a.localeCompare(b);});}
function updateGauges(cpu,cfg){const w=document.getElementById('gauges');const names=sortGaugeNames(Object.keys(cpu||{}));if(!names.length){for(const [name,node] of gaugeNodes){if(node&&node.parentElement)node.remove();gaugeNodes.delete(name);}seenGaugeInstances.clear();w.innerHTML='<div class="empty">Sin instancias</div>';return;}const desired=new Set(names);const empty=w.querySelector('.empty');if(empty)empty.remove();for(const [name,node] of gaugeNodes){if(desired.has(name))continue;if(!node.classList.contains('leaving')){node.classList.add('leaving');setTimeout(()=>{const curr=gaugeNodes.get(name);if(curr===node){if(node.parentElement)node.remove();gaugeNodes.delete(name);seenGaugeInstances.delete(name);if(!gaugeNodes.size){w.innerHTML='<div class="empty">Sin instancias</div>';}}},330);}}for(const name of names){const value=cpu[name]||0;let node=gaugeNodes.get(name);if(!node){const isNew=gaugesBootstrapped&&!seenGaugeInstances.has(name);node=createGaugeNode(name,value,isNew);gaugeNodes.set(name,node);w.appendChild(node);}else{node.classList.remove('leaving');node.classList.remove('entering');updateGaugeNode(node,value);if(!node.parentElement){w.appendChild(node);}}seenGaugeInstances.add(name);}gaugesBootstrapped=true;}
let lastServerTime=new Date(),lastSyncTime=Date.now();
function updateClock(){const elapsed=Date.now()-lastSyncTime;const currentTime=new Date(lastServerTime.getTime()+elapsed);document.getElementById('stime').textContent=currentTime.toLocaleTimeString('es-CO');}
async function refresh(){try{const[st,cfg]=await Promise.all([api('/api/status'),api('/api/config')]);const nextStartId=String(st.start_unix_nano||st.start_unix||'');if(serviceStartId&&nextStartId&&nextStartId!==serviceStartId){resetTerminal('servicio reiniciado: reiniciando stream de logs...');}if(nextStartId)serviceStartId=nextStartId;lastServerTime=new Date(st.server_time);lastSyncTime=Date.now();document.getElementById('si').textContent=st.instances;document.getElementById('sc').textContent=st.avg_cpu.toFixed(1)+'%';document.getElementById('sb').textContent=st.backends;const s2=document.getElementById('ss2');s2.textContent=st.scaler_enabled?'ACTIVO':'PAUSADO';s2.className='sv '+(st.scaler_enabled?'cg':'cr');document.getElementById('ss3').textContent=st.scaler_enabled?'respondiendo a umbrales':'en pausa manual';document.getElementById('uptime').textContent='uptime: '+st.uptime;updateClock();document.getElementById('cu').textContent=cfg.upper_threshold+'%';document.getElementById('cl').textContent=cfg.lower_threshold+'%';document.getElementById('ci').textContent=cfg.sample_interval+'s';document.getElementById('cw').textContent=cfg.evaluation_window+'s';const badge=document.getElementById('sbadge');badge.textContent=st.scaler_enabled?'Auto-scale ON':'Auto-scale OFF';badge.className='badge '+(st.scaler_enabled?'bon':'boff');const latest=Object.assign({},st.latest_cpu||{});(st.instance_names||[]).forEach(n=>{if(latest[n]==null)latest[n]=0;});updateGauges(latest,cfg);updateChart(latest);}catch(e){console.error(e)}}
async function loadBackends(){const data=await api('/api/backends');const el=document.getElementById('bl');if(!data||!data.length){el.innerHTML='<div class="empty">Sin backends. Crea uno con "+ Nuevo Backend".</div>';return;}el.innerHTML=data.map(b=>'<div class="card" style="margin-bottom:14px"><div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:12px"><div><strong>'+b.name+'</strong><span class="tag tb" style="margin-left:7px">'+b.algorithm+'</span></div><div class="bgrp"><button class="btn bg bsm" onclick="openEB(\''+b.name+'\',\''+b.algorithm+'\')">✎ Editar</button><button class="btn bg bsm" onclick="openAS(\''+b.name+'\')">+ Servidor</button><button class="btn bd bsm" onclick="delBackend(\''+b.name+'\')">✕</button></div></div>'+renderSrv(b.name,b.servers||[])+'</div>').join('');}
function renderSrv(bk,srvs){if(!srvs.length)return'<div style="color:var(--muted);font-size:.8rem">Sin servidores.</div>';return'<div style="overflow-x:auto"><table><thead><tr><th>Nombre</th><th>IP</th><th>Puerto</th><th>Peso</th><th>Estado</th><th>Acciones</th></tr></thead><tbody>'+srvs.map(s=>'<tr><td>'+s.name+'</td><td><code style="font-family:var(--mono);font-size:.78rem">'+s.ip+'</code></td><td>'+s.port+'</td><td>'+s.weight+'</td><td><span class="tag '+(s.active?'tg':'tr')+'">'+( s.active?'activo':'inactivo')+'</span></td><td><div class="bgrp"><button class="btn bg bsm" onclick="openES(\''+bk+'\',\''+s.name+'\',\''+s.ip+'\','+s.port+','+s.weight+')">✎</button><button class="btn bd bsm" onclick="rmSrv(\''+bk+'\',\''+s.name+'\')">✕</button></div></td></tr>').join('')+'</tbody></table></div>';}
function openEB(n,a){eb=n;document.getElementById('bmt').textContent='Editar Backend';document.getElementById('bn').value=n;document.getElementById('bn').disabled=true;document.getElementById('ba').value=a;openModal('mb');}
function openAS(bk){es=null;document.getElementById('smt').textContent='Agregar Servidor';document.getElementById('sbk').value=bk;document.getElementById('son').value='';['snm','sip'].forEach(i=>document.getElementById(i).value='');document.getElementById('spt').value=8000;document.getElementById('swt').value=1;openModal('ms');}
function openES(bk,n,ip,p,w){es={bk,n};document.getElementById('smt').textContent='Editar Servidor';document.getElementById('sbk').value=bk;document.getElementById('son').value=n;document.getElementById('snm').value=n;document.getElementById('sip').value=ip;document.getElementById('spt').value=p;document.getElementById('swt').value=w;openModal('ms');}
async function saveBackend(){const n=document.getElementById('bn').value.trim(),a=document.getElementById('ba').value;if(!n){alert2('amb','Nombre requerido');return;}if(!validName(n)){alert2('amb','Nombre inválido. Usa 2-64 caracteres: letras, números, guion o guion bajo.');return;}const res=eb?await post('/api/backends/'+eb,{algorithm:a},'PUT'):await post('/api/backends',{name:n,algorithm:a});if(res.error){alert2('amb',res.error);return;}closeModal('mb');eb=null;document.getElementById('bn').disabled=false;loadBackends();}
async function delBackend(n){if(!confirm('¿Eliminar backend "'+n+'"?'))return;const res=await post('/api/backends/'+n,{},'DELETE');if(res.error)alert2('ab',res.error);else loadBackends();}
async function saveServer(){const bk=document.getElementById('sbk').value,on=document.getElementById('son').value,n=document.getElementById('snm').value.trim(),ip=document.getElementById('sip').value.trim(),p=toInt('spt',1,65535),w=toInt('swt',1,256);if(!n||!ip||p===null){alert2('ams','Nombre, IP y puerto requeridos');return;}if(!validName(n)){alert2('ams','Nombre de servidor inválido.');return;}if(!validIPv4(ip)){alert2('ams','IP inválida. Usa formato IPv4, por ejemplo 10.0.2.10');return;}if(w===null){alert2('ams','Peso inválido. Debe estar entre 1 y 256.');return;}const res=es?await post('/api/backends/'+bk+'/servers/'+on,{ip,port:p,weight:w},'PUT'):await post('/api/backends/'+bk+'/servers',{name:n,ip,port:p,weight:w});if(res.error){alert2('ams',res.error);return;}closeModal('ms');es=null;loadBackends();}
async function rmSrv(bk,n){if(!confirm('¿Eliminar servidor "'+n+'"?'))return;const res=await post('/api/backends/'+bk+'/servers/'+n,{},'DELETE');if(res.error)alert2('ab',res.error);else loadBackends();}
async function loadVMs(){const data=await api('/api/vms');const el=document.getElementById('vt');if(!data||!data.length){el.innerHTML='<div class="empty">Sin VMs registradas en VirtualBox.</div>';return;}el.innerHTML='<div class="card"><div style="overflow-x:auto"><table><thead><tr><th>Nombre</th><th>IP VM</th><th>Puerto App</th><th>Puerto SSH (host)</th><th>Estado</th><th>CPU</th><th>Creada</th></tr></thead><tbody>'+data.map(v=>{const ssh=v.ssh_port&&v.ssh_port>0?'127.0.0.1:'+v.ssh_port:'—';const app=v.port&&v.port>0?v.port:'—';const ip=v.ip&&v.ip!==''?v.ip:'—';const created=v.created_at?new Date(v.created_at).toLocaleString('es-CO'):'—';const cpu=(typeof v.cpu==='number')?v.cpu.toFixed(1)+'%':'—';return'<tr><td><strong>'+v.name+'</strong></td><td><code style="font-family:var(--mono);font-size:.78rem">'+ip+'</code></td><td>'+app+'</td><td>'+ssh+'</td><td><span class="tag '+(v.status==='running'?'tg':'tr')+'">'+v.status+'</span></td><td>'+cpu+'</td><td style="font-size:.78rem;color:var(--muted)">'+created+'</td></tr>';}).join('')+'</tbody></table></div></div>';}
async function loadConfig(){const cfg=await api('/api/config');document.getElementById('ut').value=cfg.upper_threshold;document.getElementById('uv').textContent=cfg.upper_threshold+'%';document.getElementById('lt').value=cfg.lower_threshold;document.getElementById('lv').textContent=cfg.lower_threshold+'%';document.getElementById('pt').value=cfg.peak_threshold||90;document.getElementById('pv').textContent=(cfg.peak_threshold||90)+'%';document.getElementById('iv').value=cfg.sample_interval;document.getElementById('ev').value=cfg.evaluation_window;document.getElementById('mx').value=Math.max(2,cfg.max_instances||2);document.getElementById('mn').value=Math.max(2,cfg.min_instances||2);}
async function saveConfig(){const upper=toFloat('ut',30,99),lower=toFloat('lt',1,60),peak=toFloat('pt',50,100),sample=toInt('iv',5,300),windowSec=toInt('ev',10,600),minInstances=toInt('mn',2,20),maxInstances=toInt('mx',2,20);if([upper,lower,peak,sample,windowSec,minInstances,maxInstances].some(v=>v===null)){alert2('ac','Hay campos inválidos en la configuración.');return;}if(lower>=upper){alert2('ac','El umbral inferior debe ser menor al superior.');return;}if(peak<upper){alert2('ac','El umbral pico debe ser mayor o igual al umbral superior.');return;}if(windowSec<sample){alert2('ac','La ventana de evaluación no puede ser menor al intervalo de muestreo.');return;}if(minInstances>maxInstances){alert2('ac','El mínimo de instancias no puede ser mayor al máximo.');return;}const body={upper_threshold:upper,lower_threshold:lower,peak_threshold:peak,sample_interval:sample,evaluation_window:windowSec,max_instances:maxInstances,min_instances:minInstances};const res=await post('/api/config',body,'PUT');if(res.error)alert2('ac',res.error);else alert2('ac','Configuración guardada.','success');}
async function scalerToggle(on){await post('/api/autoscaler/'+(on?'enable':'disable'),{});alert2('ac','Auto-scaler '+(on?'activado':'pausado')+'.','success');refresh();}
async function loadTerminalLogs(forceFull=false){if(!forceFull&&terminalStreamConnected)return;if(terminalPolling)return;terminalPolling=true;try{const data=await api('/api/events');if(!data||!data.length){if(!terminalLines.length)renderTerminal();return;}const ordered=data.slice(0,80).reverse();if(forceFull||terminalSeenKeys.size===0){terminalSeenKeys.clear();terminalKeyOrder.length=0;terminalLines.length=0;ordered.forEach(e=>appendTerminalEvent(e));renderTerminal();return;}let appended=false;ordered.forEach(e=>{const key=eventKey(e);if(!terminalSeenKeys.has(key)){appendTerminalEvent(e);appended=true;}});if(appended)renderTerminal();}catch(e){console.error(e)}finally{terminalPolling=false}}
async function loadHAP(){const data=await api('/api/haproxy/config');document.getElementById('hraw').textContent=data.config||'(vacío)';}
async function runSim(){const cpu=toFloat('ssp',10,100),duration=toInt('ssd',10,600);if(cpu===null||duration===null){alert2('as2','Parámetros inválidos para stress-ng.');return;}const body={cpu_percent:cpu,duration:duration,use_ssh:true};const res=await post('/api/simulate',body);if(res.error)alert2('as2',res.error);else alert2('as2','stress-ng global iniciado.','success');}
async function cancelSim(){const body={use_ssh:true};const res=await post('/api/simulate/cancel',body);if(res.error)alert2('as2',res.error);else alert2('as2','stress-ng global cancelado.','success');}
async function runWrk(){const threads=toInt('wt',1,64),connections=toInt('wc',1,2000),duration=toInt('wd',1,86400);let path=(document.getElementById('wp').value||'').trim();if(path==='')path='/heavy?ms=6000';if(!validPath(path)){alert2('as2','Ruta inválida. Debe iniciar con / y no incluir caracteres peligrosos.');return;}if([threads,connections,duration].some(v=>v===null)){alert2('as2','Parámetros inválidos para wrk.');return;}const body={threads:threads,connections:connections,duration:duration,path:path};const res=await post('/api/simulate/wrk',body);if(res.error)alert2('as2',res.error);else alert2('as2','wrk iniciado vía HAProxy.','success');}
async function cancelWrk(){const res=await post('/api/simulate/wrk/cancel',{});if(res.error)alert2('as2',res.error);else alert2('as2','wrk cancelado.','success');}
async function tick(){if(uiTickRunning)return;uiTickRunning=true;try{await refresh();const active=document.querySelector('.page.active')?.id;if(active==='page-vms')await loadVMs();if(active==='page-backends')await loadBackends();if(active==='page-haproxy')await loadHAP();}catch(e){console.error(e)}finally{uiTickRunning=false}}
tick();loadTerminalLogs(true);connectTerminalStream();setInterval(updateClock,1000);setInterval(tick,1000);setInterval(()=>loadTerminalLogs(false),700);
</script>
</body></html>`

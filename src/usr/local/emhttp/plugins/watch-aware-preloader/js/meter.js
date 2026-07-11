'use strict';
// Watch-Aware Preloader budget meter. The projection math lives in the Go engine
// (-estimate); this only re-aggregates the precomputed, anonymized rows the page
// embeds, so the meter reacts instantly to control changes. No item identity is
// ever present here - rows are {u,t,l,b,r} only.

// wapAggregate filters the rows by the current selection, applies each
// (user,tier) max-items cap in global-rank order, sums the projected bytes, and
// finds the budget cutline (the engine warms in rank order and stops when the
// byte budget is exhausted, so everything past the cutline will not warm).
// sel = { users:Set<string>, libraries:Set<string>,
//         tiers:{ 'resume':{enabled,max}, 'next-up':{...}, 'recently-added':{...} } }
// An empty users/libraries Set means "all" (matches the engine's empty-list rule).
function wapAggregate(rows, budgetBytes, sel) {
  const allUsers = sel.users.size === 0;
  const allLibs = sel.libraries.size === 0;
  const kept = rows.filter(function (r) {
    const tier = sel.tiers[r.t];
    return (allUsers || sel.users.has(r.u)) &&
           (allLibs || sel.libraries.has(r.l)) &&
           tier && tier.enabled;
  });

  // Per-(user,tier) cap, applied in rank order (keep the highest-priority items).
  const buckets = Object.create(null);
  for (let i = 0; i < kept.length; i++) {
    const r = kept[i];
    const key = r.u + '|' + r.t;
    (buckets[key] || (buckets[key] = [])).push(r);
  }
  let capped = [];
  for (const key in buckets) {
    const g = buckets[key].sort(function (a, b) { return a.r - b.r; });
    const tier = key.split('|')[1];
    const max = (sel.tiers[tier].max | 0);
    capped = capped.concat(max > 0 ? g.slice(0, max) : g);
  }

  // Global rank order for the budget cutline.
  capped.sort(function (a, b) { return a.r - b.r; });

  let cum = 0, projected = 0, cutIndex = -1;
  for (let i = 0; i < capped.length; i++) {
    projected += capped[i].b;
    cum += capped[i].b;
    if (cutIndex < 0 && cum > budgetBytes) {
      cutIndex = i; // this item is the first that overruns the budget
    }
  }
  const dropByTier = {};
  let dropCount = 0;
  if (cutIndex >= 0) {
    for (let i = cutIndex; i < capped.length; i++) {
      dropByTier[capped[i].t] = (dropByTier[capped[i].t] || 0) + 1;
      dropCount++;
    }
  }
  return {
    projected: projected,
    budget: budgetBytes,
    over: projected > budgetBytes,
    count: capped.length,
    dropCount: dropCount,
    dropByTier: dropByTier,
  };
}

if (typeof module !== 'undefined' && module.exports) {
  module.exports = { wapAggregate: wapAggregate };
}

// --- DOM layer (browser only) -------------------------------------------------
// Reads the embedded estimate island + live form state, repaints #wap-meter on
// every control change. The control tier keys map to the estimate tier tokens.
var WAP_TIER_KEYS = { RESUME: 'resume', NEXTUP: 'next-up', RECENT: 'recently-added' };

function wapCheckedValues(name) {
  var set = new Set();
  var els = document.querySelectorAll('input[name="' + name + '"]:checked');
  for (var i = 0; i < els.length; i++) { set.add(els[i].value); }
  return set;
}

function wapReadSelection() {
  var tiers = {};
  Object.keys(WAP_TIER_KEYS).forEach(function (k) {
    var en = document.querySelector('input[name="TIER_' + k + '_ENABLED"]');
    var mx = document.querySelector('input[name="TIER_' + k + '_MAX"]');
    tiers[WAP_TIER_KEYS[k]] = {
      enabled: en ? en.checked : true,
      max: mx ? (parseInt(mx.value, 10) || 0) : 0,
    };
  });
  return { users: wapCheckedValues('USERS[]'), libraries: wapCheckedValues('LIBRARIES[]'), tiers: tiers };
}

function wapFmtGiB(bytes) { return (bytes / 1073741824).toFixed(1) + ' GiB'; }

// wapTierLabel capitalizes a tier token for display ("resume" -> "Resume",
// "recently-added" -> "Recently-added").
function wapTierLabel(t) { return t ? t.charAt(0).toUpperCase() + t.slice(1) : t; }

function wapPaint(est) {
  var meter = document.getElementById('wap-meter');
  if (!meter) { return; }
  // budget_bytes can be 0 if the engine could not read available RAM. Treat that
  // as "budget unavailable": show projected bytes only, no percentage/over/drops.
  var hasBudget = est.budget_bytes > 0;
  var a = wapAggregate(est.rows || [], est.budget_bytes || 0, wapReadSelection());
  var pct = hasBudget ? (a.projected / est.budget_bytes) * 100 : 0;
  var over = hasBudget && a.over;
  var state = !hasBudget ? 'ok' : (pct > 100 ? 'over' : (pct > 90 ? 'caution' : 'ok'));
  var bar = meter.querySelector('.wap-bar-fill');
  bar.style.width = Math.min(pct, 100).toFixed(1) + '%';
  meter.setAttribute('data-state', state);
  var overEl = meter.querySelector('.wap-over-fill');
  overEl.style.width = pct > 100 ? Math.min(pct - 100, 100).toFixed(1) + '%' : '0';

  var line = wapFmtGiB(a.projected) + ' projected';
  if (hasBudget) {
    line += ' of ' + wapFmtGiB(est.budget_bytes) + ' budget';
    if (over) { line += ' (over by ' + wapFmtGiB(a.projected - est.budget_bytes) + ')'; }
  } else {
    line += ' (budget unavailable)';
  }
  meter.querySelector('.wap-meter-text').textContent = line;

  var drop = meter.querySelector('.wap-drop');
  if (over && a.dropCount > 0) {
    var parts = Object.keys(a.dropByTier).map(function (t) { return wapTierLabel(t) + ' ' + a.dropByTier[t]; });
    drop.textContent = a.dropCount + ' items past the cutline won’t warm — ' + parts.join(', ');
    drop.style.display = '';
  } else {
    drop.style.display = 'none';
  }
}

function wapInitMeter() {
  var island = document.getElementById('wap-estimate');
  var meter = document.getElementById('wap-meter');
  if (!island || !meter) { return; }
  var est;
  try { est = JSON.parse(island.textContent || '{}'); } catch (e) { return; }
  if (!est || !est.rows) { return; }
  var repaint = function () { wapPaint(est); };
  var inputs = document.querySelectorAll(
    'input[name="USERS[]"], input[name="LIBRARIES[]"], ' +
    'input[name^="TIER_"][name$="_ENABLED"], input[name^="TIER_"][name$="_MAX"]'
  );
  for (var i = 0; i < inputs.length; i++) {
    inputs[i].addEventListener('change', repaint);
    inputs[i].addEventListener('input', repaint);
  }
  repaint(); // initial paint from the saved selection
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', wapInitMeter);
  } else {
    wapInitMeter();
  }
}

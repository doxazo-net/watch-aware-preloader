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

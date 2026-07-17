// Client-side docs search over /searchindex.json (generated at deploy
// time by Hugo -- layouts/index.searchindex.json). No service, no
// uploads: fetch the index lazily on first use, score locally.
(function () {
  var input = document.getElementById("site-search-input");
  var list = document.getElementById("site-search-results");
  if (!input || !list) return;

  var index = null;
  var loading = false;
  var active = -1;

  function load() {
    if (index || loading) return;
    loading = true;
    fetch("/searchindex.json")
      .then(function (r) { return r.json(); })
      .then(function (data) {
        index = data.map(function (p) {
          return {
            title: p.title, url: p.url, desc: p.desc || "", ref: p.ref,
            ltitle: p.title.toLowerCase(),
            ldesc: (p.desc || "").toLowerCase(),
            lbody: (p.body || "").toLowerCase(),
            body: p.body || "",
          };
        });
        search();
      });
  }

  function count(hay, needle) {
    var n = 0, i = hay.indexOf(needle);
    while (i !== -1 && n < 5) { n++; i = hay.indexOf(needle, i + needle.length); }
    return n;
  }

  function score(page, terms) {
    var total = 0;
    for (var i = 0; i < terms.length; i++) {
      var t = terms[i], s = 0;
      if (page.ltitle.indexOf(t) !== -1) s += 20;
      if (page.ldesc.indexOf(t) !== -1) s += 8;
      s += count(page.lbody, t);
      if (s === 0) return 0; // every term must match somewhere
      total += s;
    }
    return total;
  }

  function snippet(page, term) {
    var i = page.lbody.indexOf(term);
    if (i === -1) return page.desc;
    var start = Math.max(0, i - 50);
    var end = Math.min(page.body.length, i + term.length + 70);
    return (start > 0 ? "…" : "") + page.body.slice(start, end).trim() +
      (end < page.body.length ? "…" : "");
  }

  function search() {
    var q = input.value.trim().toLowerCase();
    active = -1;
    if (!q || !index) { close(); return; }
    var terms = q.split(/\s+/);
    var hits = index
      .map(function (p) { return { p: p, s: score(p, terms) }; })
      .filter(function (h) { return h.s > 0; })
      .sort(function (a, b) { return b.s - a.s; })
      .slice(0, 8);
    if (!hits.length) { close(); return; }
    list.innerHTML = "";
    hits.forEach(function (h) {
      var li = document.createElement("li");
      var a = document.createElement("a");
      a.href = h.p.url;
      var strong = document.createElement("strong");
      strong.textContent = h.p.title;
      a.appendChild(strong);
      if (h.p.ref) {
        var crumb = document.createElement("span");
        crumb.className = "search-crumb";
        crumb.textContent = "reference";
        a.appendChild(crumb);
      }
      var snip = document.createElement("span");
      snip.className = "search-snippet";
      snip.textContent = snippet(h.p, terms[0]);
      a.appendChild(snip);
      li.appendChild(a);
      list.appendChild(li);
    });
    list.hidden = false;
  }

  function close() { list.hidden = true; list.innerHTML = ""; active = -1; }

  function move(delta) {
    var items = list.querySelectorAll("li");
    if (!items.length) return;
    active = (active + delta + items.length) % items.length;
    items.forEach(function (li, i) { li.classList.toggle("active", i === active); });
  }

  input.addEventListener("focus", load);
  input.addEventListener("input", function () { load(); search(); });
  input.addEventListener("keydown", function (e) {
    if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); move(-1); }
    else if (e.key === "Enter") {
      var pick = list.querySelector("li.active a") || list.querySelector("li a");
      if (pick) { window.location.href = pick.href; }
    } else if (e.key === "Escape") { close(); input.blur(); }
  });
  document.addEventListener("click", function (e) {
    if (!input.contains(e.target) && !list.contains(e.target)) close();
  });
})();

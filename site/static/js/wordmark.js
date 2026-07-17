// Stylize in-prose mentions of "byre": wrap the bare word in
// <span class="byre-mark"> so CSS can mark it. Prose only -- code
// blocks, links, and headings keep their plain text, and no-JS readers
// simply see the plain word.
(function () {
  var SKIP = { CODE: 1, PRE: 1, A: 1, SCRIPT: 1, STYLE: 1, H1: 1, H2: 1, H3: 1 };
  var root = document.querySelector("main");
  if (!root) return;
  var walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: function (node) {
      for (var el = node.parentElement; el && el !== root; el = el.parentElement) {
        if (SKIP[el.tagName] || el.classList.contains("byre-mark")) {
          return NodeFilter.FILTER_REJECT;
        }
      }
      return /\bbyre\b/.test(node.nodeValue)
        ? NodeFilter.FILTER_ACCEPT
        : NodeFilter.FILTER_SKIP;
    },
  });
  var targets = [];
  while (walker.nextNode()) targets.push(walker.currentNode);
  targets.forEach(function (node) {
    var parts = node.nodeValue.split(/\b(byre)\b/);
    if (parts.length < 2) return;
    var frag = document.createDocumentFragment();
    parts.forEach(function (part) {
      if (part === "byre") {
        var span = document.createElement("span");
        span.className = "byre-mark";
        span.textContent = "byre";
        frag.appendChild(span);
      } else if (part) {
        frag.appendChild(document.createTextNode(part));
      }
    });
    node.parentNode.replaceChild(frag, node);
  });
})();

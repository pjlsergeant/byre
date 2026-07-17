// Demo casts: create an asciinema player per .demo-cast div (shortcode:
// layouts/shortcodes/demo.html). The poster is the recording's final frame
// (P11) — the exact duration rides the data attributes, written by the
// recording harness alongside each cast. The terminal face is Comic Shanns
// Mono with Byre Term Symbols (a JuliaMono subset) behind it for
// box-drawing/arrows/dingbats; both are awaited before the player measures
// its cell grid, so glyph metrics never shift under a live terminal.
document.addEventListener("DOMContentLoaded", function () {
  var stack = '"Comic Shanns Mono", "Byre Term Symbols", ui-monospace, monospace';
  var faces = ['1em "Comic Shanns Mono"', '1em "Byre Term Symbols"'];
  Promise.all(faces.map(function (f) { return document.fonts.load(f); }))
    .catch(function () {}) // a missing face falls back; never block the demo
    .then(function () {
      document.querySelectorAll(".demo-cast").forEach(function (el) {
        AsciinemaPlayer.create(el.dataset.cast, el, {
          cols: parseInt(el.dataset.cols, 10),
          rows: parseInt(el.dataset.rows, 10),
          poster: "npt:" + el.dataset.duration,
          preload: true,
          fit: "width",
          terminalFontFamily: stack,
        });
      });
    });
});

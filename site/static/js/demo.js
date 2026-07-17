// Demo casts: create an asciinema player per .demo-cast div (shortcode:
// layouts/shortcodes/demo.html). The poster is the recording's final frame
// (P11) — the exact duration rides the data attributes, written by the
// recording harness alongside each cast.
document.addEventListener("DOMContentLoaded", function () {
  document.querySelectorAll(".demo-cast").forEach(function (el) {
    AsciinemaPlayer.create(el.dataset.cast, el, {
      cols: parseInt(el.dataset.cols, 10),
      rows: parseInt(el.dataset.rows, 10),
      poster: "npt:" + el.dataset.duration,
      preload: true,
      fit: "width",
    });
  });
});

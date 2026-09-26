// Runs in <head> before the page paints, so the right theme is there from the first frame.
// The preference is "light", "dark" or "auto" (follow the system); anything else means auto.
(function () {
  var pref = "auto";
  try {
    var saved = localStorage.getItem("wasabot-theme");
    if (saved === "light" || saved === "dark") pref = saved;
  } catch (e) {}

  var dark = pref === "dark" ||
    (pref === "auto" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
  var root = document.documentElement;
  root.setAttribute("data-theme", dark ? "dark" : "light");
  root.setAttribute("data-theme-pref", pref);
})();

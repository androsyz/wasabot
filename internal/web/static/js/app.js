(function () {
  "use strict";
  // ---- light / dark ----
  // The theme follows the system until the user picks one. Picking the theme the system already has
  // goes back to following it, so the page keeps tracking the system for people who never really chose.
  var KEY = "wasabot-theme";
  var root = document.documentElement;
  var system = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;

  function systemTheme() { return system && system.matches ? "dark" : "light"; }

  function savedPref() {
    try {
      var v = localStorage.getItem(KEY);
      return v === "light" || v === "dark" ? v : "auto";
    } catch (e) { return "auto"; }
  }

  function savePref(pref) {
    try {
      if (pref === "auto") localStorage.removeItem(KEY);
      else localStorage.setItem(KEY, pref);
    } catch (e) {}
  }

  function applyPref(pref) {
    var theme = pref === "auto" ? systemTheme() : pref;
    root.setAttribute("data-theme", theme);
    root.setAttribute("data-theme-pref", pref);
    document.querySelectorAll("[data-theme-toggle]").forEach(function (b) {
      b.setAttribute("aria-checked", String(theme === "dark"));
    });
    var color = document.querySelector('meta[name="theme-color"]');
    if (color) color.setAttribute("content", theme === "dark" ? "#161915" : "#eeede4");
  }

  document.addEventListener("click", function (e) {
    if (!e.target.closest("[data-theme-toggle]")) return;
    var next = root.getAttribute("data-theme") === "dark" ? "light" : "dark";
    var pref = next === systemTheme() ? "auto" : next;
    savePref(pref);
    applyPref(pref);
  });

  if (system) {
    var onSystemChange = function () { if (savedPref() === "auto") applyPref("auto"); };
    if (system.addEventListener) system.addEventListener("change", onSystemChange);
    else if (system.addListener) system.addListener(onSystemChange); // older Safari
  }

  // another tab changed the preference
  window.addEventListener("storage", function (e) {
    if (e.key === KEY || e.key === null) applyPref(savedPref());
  });

  applyPref(savedPref());

  // a select that submits its form when changed (the client picker)
  document.addEventListener("change", function (e) {
    var el = e.target.closest("[data-autosubmit]");
    if (el && el.form) el.form.submit();
  });

  // ask before a destructive form is sent
  document.addEventListener("submit", function (e) {
    var message = e.target.getAttribute("data-confirm");
    if (message && !window.confirm(message)) e.preventDefault();
  });

  // a button that dismisses the panel above the table
  document.addEventListener("click", function (e) {
    var close = e.target.closest("[data-close-panel]");
    if (close) close.closest("#panel").replaceChildren();
  });

  // password strength: 0 (empty) to 4
  function strength(pw) {
    if (!pw) return 0;
    var s = 0;
    if (pw.length >= 12) s++;
    if (pw.length >= 16) s++;
    if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) s++;
    if (/\d/.test(pw) && /[^A-Za-z0-9]/.test(pw)) s++;
    return Math.max(1, s);
  }
  document.addEventListener("input", function (e) {
    var input = e.target.closest("[data-strength]");
    if (!input) return;
    var meter = document.getElementById(input.getAttribute("data-strength"));
    if (meter) meter.setAttribute("data-score", String(strength(input.value)));
  });

  // six single-digit boxes that behave like one field
  document.querySelectorAll("[data-code]").forEach(function (group) {
    var boxes = Array.prototype.slice.call(group.querySelectorAll("input"));
    boxes.forEach(function (box, i) {
      box.addEventListener("input", function () {
        box.value = box.value.replace(/\D/g, "").slice(-1);
        if (box.value && boxes[i + 1]) boxes[i + 1].focus();
      });
      box.addEventListener("keydown", function (e) {
        if (e.key === "Backspace" && !box.value && boxes[i - 1]) boxes[i - 1].focus();
      });
      box.addEventListener("paste", function (e) {
        var digits = (e.clipboardData.getData("text") || "").replace(/\D/g, "");
        if (!digits) return;
        e.preventDefault();
        boxes.forEach(function (b, j) { b.value = digits[j] || ""; });
        (boxes[Math.min(digits.length, boxes.length - 1)]).focus();
      });
    });
  });

})();

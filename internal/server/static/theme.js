// Applies the saved theme before first paint, so this loads without defer.
// Dark is the default. Without storage or JavaScript the page stays dark.
(function () {
  "use strict";
  var root = document.documentElement;
  var KEY = "goat-theme";

  function read() {
    try { return localStorage.getItem(KEY); } catch (e) { return null; }
  }
  function save(value) {
    try { localStorage.setItem(KEY, value); } catch (e) { /* lasts until reload */ }
  }

  if (read() === "light") root.dataset.theme = "light";

  document.addEventListener("DOMContentLoaded", function () {
    var button = document.getElementById("sp-theme");
    function label() {
      button.textContent = root.dataset.theme === "light" ? "☀" : "☾";
    }
    button.hidden = false;
    label();
    button.addEventListener("click", function () {
      if (root.dataset.theme === "light") {
        delete root.dataset.theme;
        save("dark");
      } else {
        root.dataset.theme = "light";
        save("light");
      }
      label();
    });
  });
})();

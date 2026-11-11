// Refreshes the stats in place, and opens links in a frame under the header.
// Without JavaScript the page is still complete as loaded: links open normally,
// and a browser reload updates the stats.
(function () {
  "use strict";

  var live = document.getElementById("sp-live");
  var conn = document.getElementById("sp-conn");
  var frame = document.getElementById("sp-frame");
  var menu = document.getElementById("sp-menu");
  var current = document.getElementById("sp-current");
  var popout = document.getElementById("sp-popout");
  var seconds = parseInt(document.body.dataset.refresh, 10) || 30;
  var site = document.body.dataset.site || "";
  var baseTitle = document.title;

  function notice(text) {
    conn.textContent = text;
    conn.hidden = !text;
  }

  // ----- Frame -------------------------------------------------------------
  // The address bar carries #<id> while an app is open, so reload, Back and
  // bookmarks work. Ids come from the server; the menu lists every one.

  function framedLink(id) {
    if (!menu || !id) return null;
    return Array.prototype.find.call(menu.querySelectorAll("a[data-frame]"), function (a) {
      return a.dataset.frame === id;
    }) || null;
  }

  // Changing src would add a history entry inside the frame, so Back would
  // step through the frame instead of leaving the app. replace() adds none.
  function load(url) {
    try {
      frame.contentWindow.location.replace(url);
    } catch (e) {
      frame.src = url;
    }
  }

  function show() {
    var id = decodeURIComponent(location.hash.slice(1));
    var link = framedLink(id);
    if (menu) {
      menu.querySelectorAll("a[aria-current]").forEach(function (a) { a.removeAttribute("aria-current"); });
    }
    if (!link) {
      // The dashboard, or an anchor on it such as #observability.
      if (document.body.classList.contains("framed")) {
        document.body.classList.remove("framed");
        load("about:blank");
        delete frame.dataset.open;
        current.hidden = true;
        popout.hidden = true;
        document.title = baseTitle;
        refresh();
      }
      return;
    }
    var name = link.textContent.trim();
    if (frame.dataset.open !== id) {
      load(link.href);
      frame.dataset.open = id;
    }
    frame.title = name;
    link.setAttribute("aria-current", "page");
    current.textContent = name;
    current.hidden = false;
    popout.href = link.href;
    popout.hidden = false;
    document.title = name + " · " + site;
    document.body.classList.add("framed");
  }

  document.addEventListener("click", function (e) {
    var a = e.target.closest && e.target.closest("a");
    if (!a) return;
    if (menu && menu.open && menu.contains(a)) menu.open = false;
    // Let the browser handle new-tab and new-window clicks as usual.
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    if (a.dataset.frame) {
      e.preventDefault();
      if (location.hash.slice(1) === a.dataset.frame) {
        show();
      } else {
        location.hash = a.dataset.frame;
      }
    } else if (a.getAttribute("href") === "/" && a.target !== "_blank") {
      e.preventDefault();
      if (location.hash) history.pushState(null, "", location.pathname + location.search);
      window.scrollTo(0, 0);
      show();
    }
  });

  // Close the menu on a click elsewhere or Escape.
  document.addEventListener("click", function (e) {
    if (menu && menu.open && !menu.contains(e.target)) menu.open = false;
  });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && menu && menu.open) {
      menu.open = false;
      menu.querySelector("summary").focus();
    }
  });

  window.addEventListener("hashchange", show);
  window.addEventListener("popstate", show);

  // ----- Live stats --------------------------------------------------------

  async function refresh() {
    // Nobody sees the stats while an app is open, so don't fetch them.
    if (document.hidden || document.body.classList.contains("framed")) return;
    var res;
    try {
      res = await fetch("/api/live", { credentials: "same-origin", cache: "no-store" });
    } catch (e) {
      notice("Not updating: connection lost");
      return;
    }
    if (res.status === 401 || res.status === 403) {
      // The session ended. /api/ returns 401 rather than starting a login, so
      // sign-in has to come from a full page load.
      notice("Session ended: reload to sign in");
      clearInterval(timer);
      return;
    }
    if (!res.ok) {
      notice("Not updating: server error");
      return;
    }
    var html = await res.text();

    // Replacing the content would drop keyboard focus, so put it back.
    var active = document.activeElement;
    var href = active && live.contains(active) ? active.getAttribute("href") : null;
    live.innerHTML = html;
    // Apps come and go as routes change, so the menu follows the page.
    var next = document.getElementById("sp-menu-next");
    var panel = document.getElementById("sp-menu-panel");
    if (next && panel) {
      panel.replaceChildren(next.content.cloneNode(true));
      next.remove();
    }
    if (href) {
      var again = Array.prototype.find.call(live.querySelectorAll("a[href]"), function (a) {
        return a.getAttribute("href") === href;
      });
      if (again) again.focus();
    }
    notice("");
  }

  var timer = setInterval(refresh, seconds * 1000);
  document.addEventListener("visibilitychange", refresh);
  show();
})();

// Apply the saved theme before first paint to avoid a flash. Loaded as a
// blocking script in <head>, and a file because the CSP forbids inline scripts.
try { var th = localStorage.getItem("theme"); if (th) document.documentElement.dataset.theme = th; } catch (e) {}
// Entry animation (CSS .enter) only on a full page load; app.js removes it again.
try { if (!matchMedia("(prefers-reduced-motion: reduce)").matches) document.documentElement.classList.add("enter"); } catch (e) {}

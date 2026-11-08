// Apply the saved theme before first paint to avoid a flash. Loaded as a
// blocking script in <head>, and a file because the CSP forbids inline scripts.
try { var th = localStorage.getItem("theme"); if (th) document.documentElement.dataset.theme = th; } catch (e) {}

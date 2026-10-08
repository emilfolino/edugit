// Upgrades <textarea data-editor> to the vendored Monaco editor. The
// textarea stays the form field, so without JavaScript the form still works.
const base = "/static/monaco/";

const languages = {
  js: "javascript", mjs: "javascript", ts: "typescript", json: "json",
  html: "html", css: "css", md: "markdown", py: "python", go: "go",
  java: "java", c: "c", h: "c", cpp: "cpp", rs: "rust", sh: "shell",
  yml: "yaml", yaml: "yaml", xml: "xml", sql: "sql", rb: "ruby", php: "php",
};

function loadScript(src) {
  return new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = src;
    script.onload = resolve;
    script.onerror = () => reject(new Error("cannot load " + src));
    document.head.append(script);
  });
}

async function loadMonaco() {
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = base + "vs/editor/editor.main.css";
  document.head.append(link);
  await loadScript(base + "vs/loader.js");
  const origin = location.origin + base;
  window.MonacoEnvironment = {
    getWorkerUrl: () => "data:text/javascript;charset=utf-8," + encodeURIComponent(
      `self.MonacoEnvironment = { baseUrl: "${origin}" };
importScripts("${origin}vs/loader.js");
require.config({ paths: { vs: "${origin}vs" } });
require(["vs/editor/editor.worker"]);`),
  };
  window.require.config({ paths: { vs: base + "vs" } });
  return new Promise((resolve) => window.require(["vs/editor/editor.main"], () => resolve(window.monaco)));
}

const areas = document.querySelectorAll("textarea[data-editor]");
if (areas.length > 0) {
  try {
    const monaco = await loadMonaco();
    for (const area of areas) {
      const host = document.createElement("div");
      host.className = "editor-host";
      area.after(host);
      area.hidden = true;
      const ext = (area.dataset.path ?? "").split(".").pop();
      const editor = monaco.editor.create(host, {
        value: area.value,
        language: languages[ext] ?? "plaintext",
        automaticLayout: true,
        minimap: { enabled: false },
        theme: matchMedia("(prefers-color-scheme: dark)").matches ? "vs-dark" : "vs",
      });
      area.form?.addEventListener("submit", () => {
        area.value = editor.getValue();
      });
    }
  } catch (err) {
    console.warn("editor unavailable, using the plain textarea", err);
  }
}

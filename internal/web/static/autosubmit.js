// Submits a form when a control marked data-autosubmit changes.
for (const el of document.querySelectorAll("[data-autosubmit]")) {
  el.addEventListener("change", () => el.form?.requestSubmit());
}

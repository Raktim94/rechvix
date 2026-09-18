import { invoke } from "@tauri-apps/api/core";

const form = document.querySelector<HTMLFormElement>("#server-form")!;
const urlInput = document.querySelector<HTMLInputElement>("#server-url")!;
const connectButton = document.querySelector<HTMLButtonElement>("#connect-button")!;
const errorEl = document.querySelector<HTMLParagraphElement>("#server-error")!;

// Prefills the field on "Change Server…" (reopening this same page in its
// own small window) so it isn't blank — on first run this just resolves
// to nothing and the field stays empty, which is correct there too.
invoke<string | null>("get_server_url")
  .then((saved) => {
    if (saved) urlInput.value = saved;
  })
  .catch(() => {
    // No saved URL yet (or the store genuinely has nothing) — leave the
    // field blank rather than surfacing this as an error; there's
    // nothing actionable for a first-run user here.
  });

form.addEventListener("submit", (e) => {
  e.preventDefault();
  errorEl.textContent = "";
  connectButton.disabled = true;
  connectButton.textContent = "Connecting…";

  invoke("save_server_url", { url: urlInput.value })
    .catch((err) => {
      errorEl.textContent = typeof err === "string" ? err : "Could not save that address.";
    })
    .finally(() => {
      connectButton.disabled = false;
      connectButton.textContent = "Connect";
    });
});

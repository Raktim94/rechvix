import { invoke } from "@tauri-apps/api/core";

const loadingState = document.querySelector<HTMLDivElement>("#loading-state")!;
const errorState = document.querySelector<HTMLDivElement>("#error-state")!;
const errorMessage = document.querySelector<HTMLParagraphElement>("#error-message")!;
const retryButton = document.querySelector<HTMLButtonElement>("#retry-button")!;

/// Called from Rust (`window.eval("window.showStartupError(...)")`) when
/// the bundled Postgres/server backend fails to start. There's no more
/// "which server?" settings page to fall back to, so this is the only
/// thing a failed launch shows — it has to carry enough detail to be
/// actionable (see backend.rs's StartupError::user_message).
(window as unknown as { showStartupError: (message: string) => void }).showStartupError = (
  message: string,
) => {
  loadingState.hidden = true;
  errorMessage.textContent = message;
  errorState.hidden = false;
};

retryButton.addEventListener("click", () => {
  errorState.hidden = true;
  loadingState.hidden = false;
  invoke("retry_startup").catch(() => {
    // retry_startup reports failure via showStartupError itself (same
    // path as the initial attempt); nothing extra to do here.
  });
});

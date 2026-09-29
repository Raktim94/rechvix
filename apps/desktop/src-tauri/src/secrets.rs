// AEAD_ENCRYPTION_KEY generation/persistence.
//
// The server encrypts MFA secrets and e-invoice provider credentials with
// this key (internal/platform/crypto/aead.go in the main rechvix repo). If
// it's unset, the server generates an EPHEMERAL one every process start and
// only logs a warning — anything encrypted with the previous run's key
// becomes silently unreadable. A bundled desktop app restarts constantly
// (every app launch), so this must be generated once and reused forever,
// the same way `install.sh`'s `openssl rand -base64 32` is meant to be
// generated once and kept in `.env` for a server install.
use crate::paths::AppPaths;
use base64::Engine;
use rand::RngCore;

const KEY_LEN: usize = 32;

fn is_valid(candidate: &str) -> bool {
    base64::engine::general_purpose::STANDARD
        .decode(candidate)
        .map(|bytes| bytes.len() == KEY_LEN)
        .unwrap_or(false)
}

pub fn load_or_generate_aead_key(paths: &AppPaths) -> Result<String, String> {
    if let Ok(existing) = std::fs::read_to_string(&paths.aead_key_file) {
        let trimmed = existing.trim();
        if is_valid(trimmed) {
            return Ok(trimmed.to_string());
        }
        // Corrupt or truncated file — treat as "no key" rather than a
        // hard failure, but this does silently orphan anything encrypted
        // under the previous key. Worth a log line; there's no user
        // action to take here beyond "some old secrets may need
        // re-entering," which isn't worth blocking startup over.
        eprintln!(
            "warning: {} did not contain a valid 32-byte base64 key — generating a new one",
            paths.aead_key_file.display()
        );
    }

    let mut key = [0u8; KEY_LEN];
    rand::rngs::OsRng.fill_bytes(&mut key);
    let encoded = base64::engine::general_purpose::STANDARD.encode(key);

    std::fs::write(&paths.aead_key_file, &encoded)
        .map_err(|e| format!("could not persist the encryption key: {e}"))?;

    Ok(encoded)
}

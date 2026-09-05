// The real rechvix brand mark (docs/assets/brand/logo-circle.png,
// resized to apps/web/public/logo-mark.png) — replaces the earlier
// placeholder rupee-glyph tile everywhere it was duplicated (AppShell,
// LoginPage, BootstrapPage).
export function Logo({ size = 22 }: { size?: number }) {
  return <img src="/logo-mark.png" width={size} height={size} alt="" />;
}

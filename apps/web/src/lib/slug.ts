/** Upper-case ASCII code from a free-text name, for fields that must be a short
 * identifier (branch, warehouse and SKU codes). Accented letters are folded to
 * their base letter first ("Almacén" -> "ALMACEN"); without that they were
 * dropped, producing "ALMAC-N". Letters with no ASCII base (for example Greek or
 * Hindi) are still dropped, so callers should keep a fallback for an empty result. */
export function slugCode(name: string, maxLen: number): string {
  return name
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/ß/g, "SS")
    .replace(/[øØ]/g, "O")
    .replace(/[łŁ]/g, "L")
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, maxLen);
}

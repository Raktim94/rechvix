/** "PURCHASE_INVOICE" -> "Purchase invoice". Server enums are shouted
 * snake_case; people should never have to read them. Unknown input is
 * returned tidied rather than throwing. */
export function humanize(value: string | null | undefined): string {
  if (!value) return "";
  const words = value.replace(/[_-]+/g, " ").trim().toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

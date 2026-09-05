/** Clipboard helper with a fallback for non-secure contexts (PAR-2.7).
 * navigator.clipboard is only available on https/localhost; older or insecure
 * contexts fall back to a hidden textarea + document.execCommand("copy").
 * Returns true on success. */

export async function copyText(value: string): Promise<boolean> {
  if (!value) return false;
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch {
    // Fall through to the legacy path (e.g. permissions denied).
  }
  return legacyCopy(value);
}

function legacyCopy(value: string): boolean {
  try {
    const textarea = document.createElement("textarea");
    textarea.value = value;
    textarea.setAttribute("readonly", "");
    textarea.style.position = "fixed";
    textarea.style.opacity = "0";
    document.body.appendChild(textarea);
    textarea.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(textarea);
    return ok;
  } catch {
    return false;
  }
}

/** Builds a magnet link for a torrent (PAR-2.7 / existing Copy magnet). */
export function magnetFor(hash: string, name: string): string {
  return `magnet:?xt=urn:btih:${hash}&dn=${encodeURIComponent(name)}`;
}

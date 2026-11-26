/**
 * Small, dependency-free HTML escaper shared by the Markdown renderer and the
 * syntax highlighter. It lives in its own module so neither consumer has to
 * import the other (the renderer imports the highlighter, so the highlighter
 * must not import the renderer).
 */
export const escapeHtml = (value: string): string =>
  value.replace(/[&<>"']/g, (character) => {
    switch (character) {
      case '&':
        return '&amp;'
      case '<':
        return '&lt;'
      case '>':
        return '&gt;'
      case '"':
        return '&quot;'
      default:
        return '&#39;'
    }
  })

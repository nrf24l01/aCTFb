import { defineAsyncComponent } from 'vue'
import MarkdownEditorLoading from './MarkdownEditorLoading.vue'

/**
 * Lazily loaded authoring editor.
 *
 * The Tiptap bundle is large, and no public reader needs it: `renderMarkdown`
 * is the only reader dependency. Every authoring surface imports this wrapper,
 * so the editor lives in its own chunk and is fetched only when an organizer
 * opens the Write tab. The small loading notice is not a text field, so a
 * keystroke cannot be typed into a control that is about to be replaced.
 */
export const MarkdownEditorLazy = defineAsyncComponent({
  loader: () => import('./MarkdownEditor.vue'),
  loadingComponent: MarkdownEditorLoading,
  delay: 0,
})

<script lang="ts">
  import type { Snippet } from 'svelte';
  import { navigate } from './model';
  let { href, children, current = false }: { href: string; children: Snippet; current?: boolean } = $props();
</script>
<a {href} aria-current={current ? 'page' : undefined} onclick={(event) => {
  if (event.button === 0 && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
    event.preventDefault(); navigate(href);
  }
}}>{@render children()}</a>

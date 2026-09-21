<script lang="ts">
  import type { Load } from './model';
  let { load }: { load: Load<unknown> } = $props();
</script>

{#if load.status !== 'ready'}
  <div class="notice" role="status">
    <strong
      >{load.status === 'loading'
        ? 'Loading authoritative state…'
        : load.status === 'denied'
          ? 'Permission denied'
          : 'Unavailable'}</strong
    >{#if load.error}<p>{load.error}. Refresh to read current evidence.</p>{/if}
  </div>
{/if}

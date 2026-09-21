<script lang="ts">
  let { value }: { value: string | null | undefined } = $props();
  const green = ['ready', 'fresh', 'passed', 'applied', 'confirmed', 'healthy', 'succeeded', 'complete'];
  const red = ['failed', 'conflict', 'rollback-conflict', 'unavailable', 'denied', 'blocked'];
</script>
<span class="badge" data-tone={green.includes(value ?? '') ? 'good' : red.includes(value ?? '') ? 'bad' : 'neutral'}>{value ?? 'Unknown'}</span>

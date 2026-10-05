<script lang="ts">
  let { value, neutral = false }: { value: string | null | undefined; neutral?: boolean } = $props();
  const green = [
    'ready',
    'fresh',
    'passed',
    'applied',
    'confirmed',
    'healthy',
    'succeeded',
    'complete',
  ];
  const red = [
    'failed',
    'conflict',
    'rollback-conflict',
    'unavailable',
    'denied',
    'blocked',
  ];
</script>

<span
  class="badge"
  data-tone={neutral ? 'neutral' : green.includes(value ?? '')
    ? 'good'
    : red.includes(value ?? '')
      ? 'bad'
      : 'neutral'}>{value ?? 'Unknown'}</span
>

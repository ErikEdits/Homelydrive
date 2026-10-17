<script lang="ts">
  let { size = 32 }: { size?: number } = $props()
</script>

<svg width={size} height={size} viewBox="0 0 512 512" aria-hidden="true">
  <defs>
    <linearGradient id="hd-bg" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="#4f8ef7" />
      <stop offset="1" stop-color="#1f56c9" />
    </linearGradient>
  </defs>
  <rect width="512" height="512" rx="116" fill="url(#hd-bg)" />
  <path fill="#fff" d="M150 382h216c48 0 86-38 86-86 0-45-34-82-78-86-12-58-63-100-122-100-50 0-93 30-112 74-48 6-86 47-86 97 0 55 45 101 100 101z" />
  <path fill="#1f56c9" d="M256 196 172 266c-6 5-2 14 6 14h14v74c0 8 6 14 14 14h100c8 0 14-6 14-14v-74h14c8 0 12-9 6-14z" />
  <rect x="238" y="300" width="36" height="68" rx="6" fill="#fff" />
</svg>

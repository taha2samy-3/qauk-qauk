<script setup lang="ts">
import { computed } from 'vue'
import { withBase } from 'vitepress'

/** Animated 3D duck; visitors who prefer reduced motion get the still twin. */
const props = withDefaults(defineProps<{ variant?: 'wave' | 'welcome' | 'hi'; width?: number; alt?: string }>(), {
  variant: 'wave',
  width: 320,
  alt: 'A friendly duck waving hello',
})
const animated = computed(() => withBase(`/ducks-3d/duck-3d-${props.variant}.svg`))
const still = computed(() => withBase(`/ducks-3d/duck-3d-${props.variant}-still.svg`))
</script>

<template>
  <picture class="waving-duck">
    <source :srcset="still" media="(prefers-reduced-motion: reduce)" />
    <img :src="animated" :alt="alt" :width="width" loading="eager" decoding="async" />
  </picture>
</template>

<style scoped>
.waving-duck {
  display: flex;
  justify-content: center;
}
.waving-duck img {
  max-width: 100%;
  height: auto;
}
</style>

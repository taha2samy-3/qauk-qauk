import DefaultTheme from 'vitepress/theme'
import { withBase, type Theme } from 'vitepress'
import { h } from 'vue'
import WavingDuck from './WavingDuck.vue'
import './custom.css'

export default {
  extends: DefaultTheme,
  Layout: () =>
    h(DefaultTheme.Layout, null, {
      // The home hero: an animated 3D duck waving at visitors.
      'home-hero-image': () => h(WavingDuck, { variant: 'welcome', width: 440, alt: 'A duck waving next to a Welcome sign' }),
    }),
  enhanceApp({ app }) {
    // Usable in any markdown page: <WavingDuck variant="hi" />
    app.component('WavingDuck', WavingDuck)
    // $withBase('/file') in raw HTML links: prefixes the GitHub Pages sub-path
    app.config.globalProperties.$withBase = withBase
  },
} satisfies Theme

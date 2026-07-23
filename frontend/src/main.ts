import '@fontsource-variable/jetbrains-mono/index.css'
import '@fontsource-variable/manrope/index.css'

import { createPinia } from 'pinia'
import { createApp } from 'vue'
import { MotionPlugin } from 'motion-v'

import App from './App.vue'
import './assets/main.css'
import { i18n } from './i18n'
import router from './router'

const app = createApp(App)
app.use(createPinia())
app.use(MotionPlugin, {
  presets: {
    'fade-up': {
      initial: { opacity: 0, y: 8 },
      animate: { opacity: 1, y: 0 },
      transition: { duration: 0.22, ease: [0.16, 1, 0.3, 1] },
    },
    'fade-in': {
      initial: { opacity: 0 },
      animate: { opacity: 1 },
      transition: { duration: 0.18 },
    },
    pressable: {
      whileHover: { scale: 1.04 },
      whilePress: { scale: 0.94 },
      transition: { type: 'spring', stiffness: 500, damping: 28 },
    },
  },
})
app.use(router)
app.use(i18n)
app.mount('#app')

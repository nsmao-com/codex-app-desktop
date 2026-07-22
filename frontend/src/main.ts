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
app.use(MotionPlugin)
app.use(router)
app.use(i18n)
app.mount('#app')

import '@fontsource/ibm-plex-mono/400.css'
import '@fontsource/ibm-plex-mono/500.css'
import '@fontsource/ibm-plex-mono/600.css'
import '@fontsource/ibm-plex-mono/700.css'
import '@/style.css'

import { createApp } from 'vue'
import App from './app/App.vue'
import router from './app/router'

createApp(App).use(router).mount('#app')
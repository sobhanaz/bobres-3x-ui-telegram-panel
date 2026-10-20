import { createApp } from 'vue'
import App from './App.vue'
import './style.css'
// By its file name: './sections' alone is the registry (sections.ts), not the loader of the section folders.
import './sections/index'
import { start } from './telegram'

start()
createApp(App).mount('#app')

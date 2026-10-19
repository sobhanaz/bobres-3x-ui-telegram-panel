import { createApp } from 'vue'
import App from './App.vue'
import './style.css'
import './sections'
import { start } from './telegram'

start()
createApp(App).mount('#app')

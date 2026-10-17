import { mount } from 'svelte'
import '../lib/styles.css'
import App from './App.svelte'

const app = mount(App, { target: document.getElementById('app')! })

// Installable as an app (PWA). Service workers need HTTPS or localhost; over
// plain http in the tailnet the browser simply skips this.
if ('serviceWorker' in navigator && (location.protocol === 'https:' || location.hostname === 'localhost')) {
  navigator.serviceWorker.register('/sw.js').catch(() => {})
}

export default app

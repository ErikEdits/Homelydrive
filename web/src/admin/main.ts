import { mount } from 'svelte'
import '../lib/styles.css'
import Admin from './Admin.svelte'

export default mount(Admin, { target: document.getElementById('app')! })

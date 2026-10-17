import { mount } from 'svelte'
import '../lib/styles.css'
import Client from './Client.svelte'

export default mount(Client, { target: document.getElementById('app')! })

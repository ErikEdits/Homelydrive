import { mount } from 'svelte'
import '../lib/styles.css'
import Share from './Share.svelte'

export default mount(Share, { target: document.getElementById('app')! })

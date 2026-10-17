<script lang="ts">
  import { post } from '../../lib/api'
  import { errText, i18n, setLang, t } from '../../lib/i18n.svelte'
  import Logo from '../../lib/ui/Logo.svelte'

  let { onlogin }: { onlogin: () => void } = $props()
  let username = $state('')
  let password = $state('')
  let error = $state('')
  let busy = $state(false)

  async function submit(e: Event) {
    e.preventDefault()
    error = ''
    busy = true
    try {
      await post('/auth/login', { username, password, cookie: true, device: deviceName() })
      onlogin()
    } catch (err) {
      error = (err as any).status === 401 ? t('login.wrong') : errText(err)
    } finally {
      busy = false
    }
  }

  function deviceName() {
    const ua = navigator.userAgent
    const browser = /Edg\//.test(ua) ? 'Edge' : /Firefox\//.test(ua) ? 'Firefox' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Browser'
    const os = /Windows/.test(ua) ? 'Windows' : /Android/.test(ua) ? 'Android' : /iPhone|iPad/.test(ua) ? 'iOS' : /Mac OS/.test(ua) ? 'macOS' : /Linux/.test(ua) ? 'Linux' : ''
    return `${browser} ${os}`.trim()
  }
</script>

<div class="wrap">
  <form class="card login" onsubmit={submit}>
    <div class="brand"><Logo size={56} /></div>
    <h1>Homelydrive</h1>
    <p class="muted">{t('login.subtitle')}</p>
    <label class="field">{t('login.username')}
      <input class="input" bind:value={username} autocomplete="username" required autocapitalize="off" />
    </label>
    <label class="field">{t('login.password')}
      <input class="input" type="password" bind:value={password} autocomplete="current-password" required />
    </label>
    {#if error}<p class="error-text">{error}</p>{/if}
    <button class="btn primary" style="width:100%" disabled={busy}>{t('login.submit')}</button>
    <div class="langs">
      <button type="button" class:active={i18n.lang === 'de'} onclick={() => setLang('de')}>Deutsch</button>
      <button type="button" class:active={i18n.lang === 'en'} onclick={() => setLang('en')}>English</button>
    </div>
  </form>
</div>

<style>
  .wrap {
    min-height: 100%;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
  }
  .login {
    width: 380px;
    max-width: 100%;
    padding: 36px 32px 24px;
    text-align: center;
    box-shadow: var(--shadow);
  }
  .login label {
    text-align: left;
  }
  .brand {
    display: flex;
    justify-content: center;
    margin-bottom: 12px;
  }
  h1 {
    margin-bottom: 4px;
  }
  p.muted {
    margin: 0 0 24px;
  }
  .langs {
    margin-top: 18px;
    display: flex;
    justify-content: center;
    gap: 6px;
  }
  .langs button {
    border: none;
    background: none;
    color: var(--text-3);
    cursor: pointer;
    padding: 4px 8px;
    border-radius: 6px;
  }
  .langs button.active {
    color: var(--brand);
    background: var(--brand-soft);
  }
</style>

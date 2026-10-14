// BOBRES landing page: the only script. It makes the install command's "copy" button work.
(() => {
  const buttons = document.querySelectorAll('[data-copy]')
  buttons.forEach((btn) => {
    btn.addEventListener('click', async () => {
      const src = document.getElementById(btn.dataset.copy)
      if (!src) return
      const text = src.textContent.replace(/^\s*\$\s*/, '').trim()
      let ok = false
      try {
        await navigator.clipboard.writeText(text)
        ok = true
      } catch (e) {
        // no clipboard API (an old browser, or no secure context): select the text and ask the browser to copy it
        const range = document.createRange()
        range.selectNodeContents(src)
        const sel = window.getSelection()
        sel.removeAllRanges()
        sel.addRange(range)
        try { ok = document.execCommand('copy') } catch (e2) { ok = false }
      }
      const label = btn.querySelector('.lbl')
      const status = document.getElementById('copied')
      if (ok && label) {
        const old = label.textContent
        label.textContent = btn.dataset.done || 'OK'
        if (status) status.textContent = btn.dataset.done || 'Copied'
        setTimeout(() => { label.textContent = old; if (status) status.textContent = '' }, 2200)
      }
    })
  })
})()

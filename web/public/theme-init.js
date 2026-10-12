// Apply the saved colour theme before first paint (light by default).
try {
  if (localStorage.getItem('hb-theme') === 'dark') document.documentElement.classList.add('dark')
} catch (e) {
  /* storage unavailable */
}

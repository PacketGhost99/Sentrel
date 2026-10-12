// Apply the saved colour theme before first paint (light by default).
try {
  if (localStorage.getItem('sentrel-theme') === 'dark') document.documentElement.classList.add('dark')
} catch (e) {
  /* storage unavailable */
}

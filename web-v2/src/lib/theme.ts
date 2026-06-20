import { useCallback, useEffect, useState } from 'react'

const STORAGE_KEY = 'spore.theme'
type Theme = 'dark' | 'light'

/**
 * Persistent theme toggle. Defaults to dark — Spore's identity is dark.
 *
 * Writes <html data-theme="..."> so CSS variables in index.css can swap
 * via the [data-theme="light"] selector.
 */
export function useTheme() {
  const [theme, setTheme] = useState<Theme>(() => {
    if (typeof window === 'undefined') return 'dark'
    const stored = window.localStorage.getItem(STORAGE_KEY) as Theme | null
    return stored === 'light' ? 'light' : 'dark'
  })

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    window.localStorage.setItem(STORAGE_KEY, theme)
  }, [theme])

  const toggleTheme = useCallback(() => {
    setTheme((t) => (t === 'dark' ? 'light' : 'dark'))
  }, [])

  return { theme, toggleTheme }
}

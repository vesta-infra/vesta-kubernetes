/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        surface: {
          0: '#0a0c10',
          1: '#0f1117',
          2: '#161820',
          3: '#1c1f29',
          4: '#242733',
        },
        border: {
          DEFAULT: '#2a2d3a',
          subtle: '#1e2130',
          hover: '#3a3e50',
        },
        accent: {
          DEFAULT: '#34d399',
          dim: '#059669',
          glow: '#6ee7b7',
          muted: 'rgba(52, 211, 153, 0.08)',
        },
        text: {
          primary: '#e8eaed',
          secondary: '#8b8fa3',
          tertiary: '#5c6078',
          inverse: '#0a0c10',
        },
        status: {
          running: '#34d399',
          failed: '#f87171',
          pending: '#fbbf24',
          'running-bg': 'rgba(52, 211, 153, 0.12)',
          'failed-bg': 'rgba(248, 113, 113, 0.12)',
          'pending-bg': 'rgba(251, 191, 36, 0.12)',
        },
      },
      fontFamily: {
        display: ['"DM Serif Display"', 'Georgia', 'serif'],
        body: ['"General Sans"', '"DM Sans"', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', '"Fira Code"', 'monospace'],
      },
      animation: {
        'fade-in': 'fadeIn 0.4s ease-out',
        'slide-up': 'slideUp 0.4s ease-out',
        'glow-pulse': 'glowPulse 3s ease-in-out infinite',
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(8px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        glowPulse: {
          '0%, 100%': { opacity: '0.4' },
          '50%': { opacity: '0.8' },
        },
      },
      boxShadow: {
        glow: '0 0 20px rgba(52, 211, 153, 0.15)',
        'glow-sm': '0 0 10px rgba(52, 211, 153, 0.1)',
        card: '0 1px 3px rgba(0, 0, 0, 0.3), 0 0 0 1px rgba(255, 255, 255, 0.03)',
      },
    },
  },
  plugins: [],
}

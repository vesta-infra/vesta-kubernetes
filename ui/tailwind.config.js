/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        surface: {
          0: '#07090f',
          1: '#0c1019',
          2: '#121825',
          3: '#1a2235',
          4: '#222d42',
        },
        border: {
          DEFAULT: '#1e293b',
          subtle: '#151d2e',
          hover: '#2d3f5e',
        },
        accent: {
          DEFAULT: '#f59e0b',
          dim: '#b45309',
          glow: '#fbbf24',
          muted: 'rgba(245, 158, 11, 0.08)',
        },
        text: {
          primary: '#e8ecf4',
          secondary: '#7c8ba1',
          tertiary: '#475569',
          inverse: '#07090f',
        },
        status: {
          running: '#22c55e',
          failed: '#ef4444',
          pending: '#f59e0b',
          'running-bg': 'rgba(34, 197, 94, 0.12)',
          'failed-bg': 'rgba(239, 68, 68, 0.12)',
          'pending-bg': 'rgba(245, 158, 11, 0.10)',
        },
      },
      fontFamily: {
        display: ['"Instrument Serif"', 'Georgia', 'serif'],
        body: ['"Plus Jakarta Sans"', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', '"Fira Code"', 'monospace'],
      },
      animation: {
        'fade-in': 'fadeIn 0.5s cubic-bezier(0.16, 1, 0.3, 1)',
        'slide-up': 'slideUp 0.5s cubic-bezier(0.16, 1, 0.3, 1)',
        'slide-up-delayed': 'slideUp 0.5s cubic-bezier(0.16, 1, 0.3, 1) 0.1s both',
        'glow-pulse': 'glowPulse 3s ease-in-out infinite',
        'border-glow': 'borderGlow 4s ease-in-out infinite',
        'float': 'float 8s ease-in-out infinite',
        'shimmer': 'shimmer 2.5s linear infinite',
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' },
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(12px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        glowPulse: {
          '0%, 100%': { opacity: '0.4' },
          '50%': { opacity: '1' },
        },
        borderGlow: {
          '0%, 100%': { borderColor: 'rgba(245, 158, 11, 0.15)' },
          '50%': { borderColor: 'rgba(245, 158, 11, 0.35)' },
        },
        float: {
          '0%, 100%': { transform: 'translateY(0) rotate(0deg)' },
          '33%': { transform: 'translateY(-10px) rotate(1deg)' },
          '66%': { transform: 'translateY(5px) rotate(-1deg)' },
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' },
        },
      },
      boxShadow: {
        glow: '0 0 30px rgba(245, 158, 11, 0.12), 0 0 60px rgba(245, 158, 11, 0.05)',
        'glow-sm': '0 0 15px rgba(245, 158, 11, 0.08)',
        'glow-lg': '0 0 50px rgba(245, 158, 11, 0.15), 0 0 100px rgba(245, 158, 11, 0.05)',
        card: '0 1px 3px rgba(0, 0, 0, 0.4), 0 0 0 1px rgba(255, 255, 255, 0.02)',
        'card-hover': '0 4px 20px rgba(0, 0, 0, 0.4), 0 0 0 1px rgba(245, 158, 11, 0.1)',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.03)',
      },
      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        'gradient-conic': 'conic-gradient(from 180deg at 50% 50%, var(--tw-gradient-stops))',
      },
    },
  },
  plugins: [],
}

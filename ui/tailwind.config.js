/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        // Near-black, cool-neutral surface ramp. 0 is the canvas; cards sit one
        // or two steps up so elevation reads without lightening the whole page.
        surface: {
          0: '#030407',
          1: '#07090f',
          2: '#0b0e16',
          3: '#10141d',
          4: '#171c27',
          5: '#1f2531',
        },
        border: {
          DEFAULT: '#171c27',
          subtle: '#0f131b',
          hover: '#242c3a',
          strong: '#2c3543',
        },
        accent: {
          DEFAULT: '#f59e0b',
          dim: '#b45309',
          glow: '#fbbf24',
          muted: 'rgba(245, 158, 11, 0.08)',
          soft: 'rgba(245, 158, 11, 0.12)',
        },
        text: {
          primary: '#eef1f7',
          secondary: '#98a2b3',
          tertiary: '#5b6577',
          quaternary: '#3d4554',
          inverse: '#030407',
        },
        // Status steps are lifted to the 400-weights: on a near-black surface the
        // lighter step is what clears 3:1. Reserved -- never used as a series hue.
        status: {
          running: '#34d399',
          failed: '#f87171',
          pending: '#fbbf24',
          degraded: '#fb923c',
          sleeping: '#7c8899',
          'running-bg': 'rgba(52, 211, 153, 0.10)',
          'failed-bg': 'rgba(248, 113, 113, 0.10)',
          'pending-bg': 'rgba(251, 191, 36, 0.10)',
          'degraded-bg': 'rgba(251, 146, 60, 0.10)',
          'sleeping-bg': 'rgba(124, 136, 153, 0.10)',
        },
      },
      fontFamily: {
        display: ['"Instrument Serif"', 'Georgia', 'serif'],
        body: ['"Plus Jakarta Sans"', 'system-ui', 'sans-serif'],
        mono: ['"JetBrains Mono"', '"Fira Code"', 'monospace'],
      },
      animation: {
        'fade-in': 'fadeIn 0.5s cubic-bezier(0.16, 1, 0.3, 1) both',
        'slide-up': 'slideUp 0.5s cubic-bezier(0.16, 1, 0.3, 1) both',
        'slide-up-delayed': 'slideUp 0.5s cubic-bezier(0.16, 1, 0.3, 1) 0.1s both',
        'glow-pulse': 'glowPulse 3s ease-in-out infinite',
        'border-glow': 'borderGlow 4s ease-in-out infinite',
        'float': 'float 8s ease-in-out infinite',
        'shimmer': 'shimmer 2.5s linear infinite',
        'ring-pulse': 'ringPulse 2.4s cubic-bezier(0.16, 1, 0.3, 1) infinite',
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
        ringPulse: {
          '0%': { transform: 'scale(1)', opacity: '0.5' },
          '70%, 100%': { transform: 'scale(2.6)', opacity: '0' },
        },
      },
      boxShadow: {
        glow: '0 0 30px rgba(245, 158, 11, 0.12), 0 0 60px rgba(245, 158, 11, 0.05)',
        'glow-sm': '0 0 15px rgba(245, 158, 11, 0.08)',
        'glow-lg': '0 0 50px rgba(245, 158, 11, 0.15), 0 0 100px rgba(245, 158, 11, 0.05)',
        card: '0 1px 2px rgba(0, 0, 0, 0.6), 0 10px 30px -18px rgba(0, 0, 0, 0.9)',
        'card-hover': '0 1px 2px rgba(0, 0, 0, 0.6), 0 16px 40px -20px rgba(0, 0, 0, 1)',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.035)',
        'ring-subtle': 'inset 0 0 0 1px rgba(255, 255, 255, 0.03)',
      },
      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        'gradient-conic': 'conic-gradient(from 180deg at 50% 50%, var(--tw-gradient-stops))',
      },
    },
  },
  plugins: [],
}

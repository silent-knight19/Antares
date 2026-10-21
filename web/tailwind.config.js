import { theme } from './tokens/tailwind.config.js';

/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme,
  plugins: [],
};

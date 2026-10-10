import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['app/**/*.test.ts', 'build/**/*.test.ts'],
    environment: 'node',
  },
})

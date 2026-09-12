import { defineConfig, loadEnv } from 'vite';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, '.', 'CONDUCTOR_');
  return {
    server: {
      host: '127.0.0.1',
      proxy: { '/api': { target: env.CONDUCTOR_API_URL || 'http://127.0.0.1:8080' } },
    },
  };
});

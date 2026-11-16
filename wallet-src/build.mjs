// Gera ../public/wallet.js (módulo ES minificado) com o SDK da Coinbase e o viem.
// Rodar: npm install && npm run build   (dentro de wallet-src/)
import { build } from 'esbuild';
import { statSync } from 'node:fs';

await build({
  entryPoints: ['wallet.js'],
  outfile: '../public/wallet.js',
  bundle: true,
  format: 'esm',
  platform: 'browser',
  target: 'es2022',
  minify: true,
  legalComments: 'none',
  alias: { react: './stub-react.js' },
  define: { 'process.env.NODE_ENV': '"production"', global: 'globalThis' },
  logLevel: 'info',
});
console.log('public/wallet.js:', Math.round(statSync('../public/wallet.js').size / 1024), 'KB');

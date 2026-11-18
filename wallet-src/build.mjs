// Gera ../public/wallet.js (módulo ES minificado) com o SDK da Coinbase e o viem.
// Rodar: npm install && npm run build   (dentro de wallet-src/)
import { build } from 'esbuild';
import { statSync } from 'node:fs';

// O SDK assina o header X-Wallet-Auth com host+caminho da requisição. Com o proxy do Flex
// esses valores mudariam, e a Coinbase recusaria a assinatura. Aqui devolvemos ao SDK a URL
// original (api.cdp.coinbase.com/platform) só para assinar; a requisição real continua no proxy.
const fixProxySigning = {
  name: 'fix-proxy-signing',
  setup(b) {
    b.onLoad({ filter: /cdp-api-client[\\/]dist[\\/]esm[\\/]index6\.js$/ }, async (args) => {
      const { readFileSync } = await import('node:fs');
      let src = readFileSync(args.path, 'utf8');
      const needle = 'const r = (e.baseURL ?? o.getUri()) + e.url;\n  return new URL(r);';
      if (!src.includes(needle)) throw new Error('fix-proxy-signing: trecho do SDK não encontrado (versão mudou?)');
      src = src.replace(needle, 'let r = (e.baseURL ?? o.getUri()) + e.url;\n  if (typeof location !== "undefined") r = r.replace(location.origin + "/cdp/platform", "https://api.cdp.coinbase.com/platform");\n  return new URL(r);');
      return { contents: src, loader: 'js' };
    });
  },
};

await build({
  plugins: [fixProxySigning],
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

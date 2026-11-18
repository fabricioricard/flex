// Módulo da carteira do Flex (Base Mainnet). Empacotado por build.mjs em ../public/wallet.js
// e carregado só depois do login. Toda a lógica do cofre fica em earn.js.
import {
  initialize, signInWithEmail, verifyEmailOTP, getCurrentUser, isSignedIn,
  sendEvmTransaction, signOut, createEvmKeyExportIframe, getAccessToken,
} from '@coinbase/cdp-core';
import {
  createPublicClient, http, encodeFunctionData, formatUnits, formatEther, isAddress,
} from 'viem';
import { base } from 'viem/chains';
import {
  ERC20, VAULT_ABI, parseAmount, validateDeposit, validateWithdraw, runDeposit, runWithdraw,
  parseApy, parseRate, parseActivity,
} from './earn.js';

// Rede única: Base Mainnet. USDC nativo da Circle (developers.circle.com/stablecoins/usdc-contract-addresses).
const net = {
  chain: base, cdp: 'base', label: 'Base',
  usdc: '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913',
  rpc: 'https://mainnet.base.org', explorer: 'https://basescan.org',
};
const USDC = net.usdc;
// Cofre Morpho (ERC-4626) "Steakhouse USDC" na Base. É o mesmo cofre do exemplo oficial da Coinbase
// (docs.cdp.coinbase.com ... morpho-integration). Rendimento variável, sem garantia.
const VAULT = {
  address: '0xbeeF010f9cb27031ad51e3333f9aF9C6B1228183',
  name: 'Steakhouse USDC', protocol: 'Morpho', curator: 'Steakhouse',
};
const client = createPublicClient({ chain: net.chain, transport: http(net.rpc) });
let projectId = null;
let flowId = null;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function address() {
  const u = await getCurrentUser();
  return u?.evmAccountObjects?.[0]?.address || null;
}

/** Renova a sessão da Coinbase. Se falhar, a sessão expirou e é preciso entrar de novo. */
async function ensureSession() {
  let t = null;
  try { t = await getAccessToken({ forceRefresh: true }); } catch { t = null; }
  if (!t) {
    try { await signOut(); } catch {}
    const e = new Error('Sua sessão da carteira expirou. Entre de novo com o código do e-mail.');
    e.code = 'SESSION_EXPIRED';
    throw e;
  }
}

async function estimateGasRetry(args) {
  let last;
  for (let i = 0; i < 3; i++) {
    try { return await client.estimateGas(args); } catch (e) { last = e; await sleep(1200); }
  }
  throw last;
}

/** Monta e envia uma transação EIP-1559 pela carteira da pessoa. Devolve o hash. */
async function sendTx(from, to, data, revertMsg) {
  const [nonce, fees] = await Promise.all([
    client.getTransactionCount({ address: from, blockTag: 'pending' }),
    client.estimateFeesPerGas(),
  ]);
  let gas;
  try {
    gas = await estimateGasRetry({ account: from, to, data });
  } catch (e) {
    const m = String(e?.shortMessage || e?.message || '');
    if (/revert|exceed|insufficient|allowance|transfer/i.test(m)) throw new Error(revertMsg || 'A rede recusou a operação. Nada foi movido.');
    throw e;
  }
  const r = await sendEvmTransaction({
    evmAccount: from,
    network: net.cdp,
    transaction: {
      to, data, value: 0n, nonce,
      gas: (gas * 12n) / 10n,
      maxFeePerGas: fees.maxFeePerGas,
      maxPriorityFeePerGas: fees.maxPriorityFeePerGas,
      chainId: net.chain.id, type: 'eip1559',
    },
  });
  return r.transactionHash;
}

async function confirmTx(hash) {
  let rc;
  try {
    rc = await client.waitForTransactionReceipt({ hash, timeout: 120_000, pollingInterval: 2000 });
  } catch {
    const e = new Error('A rede está demorando para confirmar. Veja o status no explorador e atualize depois.');
    e.code = 'PENDING'; e.hash = hash;
    throw e;
  }
  if (rc.status !== 'success') throw new Error('A transação falhou na rede.');
  return rc;
}

const PROXY_PATH = '/cdp/platform';

async function getJSON(url, ms = 7000) {
  const r = await fetch(url, { headers: { accept: 'application/json' }, signal: AbortSignal.timeout(ms) });
  if (!r.ok) throw new Error('HTTP ' + r.status);
  return r.json();
}

const api = {
  chain: { name: net.label, id: net.chain.id, explorer: net.explorer },
  usdc: USDC,
  vault: {
    ...VAULT,
    explorerUrl: net.explorer + '/address/' + VAULT.address,
    morphoUrl: 'https://app.morpho.org/base/vault/' + VAULT.address,
  },

  async init(id, opts = {}) {
    projectId = id;
    const cfg = { projectId: id, disableAnalytics: true, ethereum: { createOnLogin: 'eoa' } };
    // Modo proxy: as chamadas da Coinbase passam pelo domínio do Flex (cookie de 1ª parte).
    if (opts.proxy) cfg.basePath = location.origin + PROXY_PATH;
    await initialize(cfg);
    return isSignedIn();
  },
  isSignedIn,

  /** Envia o código de 6 dígitos para o e-mail. */
  async startEmail(email) {
    const r = await signInWithEmail({ email });
    flowId = r.flowId;
  },
  /** Confirma o código; devolve o endereço da carteira (criada no primeiro login). */
  async verifyCode(otp) {
    if (!flowId) throw new Error('Peça o código primeiro.');
    await verifyEmailOTP({ flowId, otp: String(otp).trim() });
    flowId = null;
    for (let i = 0; i < 8; i++) {
      const a = await address();
      if (a) return a;
      await sleep(500);
    }
    throw new Error('Carteira ainda sendo criada. Recarregue em alguns segundos.');
  },
  address,

  /** Saldos só da carteira (aba Carteira). */
  async balances(addr) {
    const [eth, usdc] = await Promise.all([
      client.getBalance({ address: addr }),
      client.readContract({ address: USDC, abi: ERC20, functionName: 'balanceOf', args: [addr] }),
    ]);
    return { eth: formatEther(eth), ethWei: eth, usdc: formatUnits(usdc, 6), usdcUnits: usdc };
  },

  /** Carteira + posição no cofre, tudo lido da blockchain. */
  async portfolio(addr) {
    const V = VAULT.address;
    const [r, eth] = await Promise.all([
      client.multicall({
        allowFailure: true,
        contracts: [
          { address: USDC, abi: ERC20, functionName: 'balanceOf', args: [addr] },
          { address: USDC, abi: ERC20, functionName: 'allowance', args: [addr, V] },
          { address: V, abi: VAULT_ABI, functionName: 'balanceOf', args: [addr] },
          { address: V, abi: VAULT_ABI, functionName: 'maxWithdraw', args: [addr] },
          { address: V, abi: VAULT_ABI, functionName: 'maxRedeem', args: [addr] },
          { address: V, abi: VAULT_ABI, functionName: 'maxDeposit', args: [addr] },
        ],
      }),
      client.getBalance({ address: addr }),
    ]);
    const ok = (i) => (r[i].status === 'success' ? r[i].result : null);
    if (ok(0) === null || ok(1) === null || ok(2) === null) throw new Error('Não foi possível ler seus saldos agora.');
    const shares = ok(2);
    const assets = shares > 0n
      ? await client.readContract({ address: V, abi: VAULT_ABI, functionName: 'convertToAssets', args: [shares] })
      : 0n;
    const maxW = ok(3);
    const withdrawable = maxW !== null && maxW > 0n && maxW < assets ? maxW : assets;
    return {
      usdc: formatUnits(ok(0), 6), usdcUnits: ok(0),
      allowanceUnits: ok(1),
      eth: formatEther(eth), ethWei: eth,
      sharesUnits: shares,
      vaultAssets: formatUnits(assets, 6), assetsUnits: assets,
      withdrawable: formatUnits(withdrawable, 6),
      maxWithdrawUnits: maxW, maxRedeemUnits: ok(4), maxDepositUnits: ok(5),
    };
  },

  validateDeposit, validateWithdraw,

  /** Valida destino e valor antes de enviar. Devolve mensagem de erro ou null. */
  validateSend(to, amount, usdcUnits) {
    if (!isAddress(to)) return 'Endereço de destino inválido.';
    const v = parseAmount(amount);
    if (v === null) return 'Valor inválido. Use no máximo 6 casas decimais.';
    if (v <= 0n) return 'Digite um valor maior que zero.';
    if (usdcUnits !== undefined && v > usdcUnits) return 'Saldo de USDC insuficiente.';
    return null;
  },

  /** Envia USDC (ERC-20) da carteira da pessoa. Ela paga a taxa em ETH. */
  async sendUsdc(to, amount) {
    await ensureSession();
    const from = await address();
    if (!from) throw new Error('Carteira não encontrada. Entre de novo.');
    const value = parseAmount(amount);
    if (value === null || value <= 0n) throw new Error('Valor inválido.');
    const data = encodeFunctionData({ abi: ERC20, functionName: 'transfer', args: [to, value] });
    return sendTx(from, USDC, data, 'A rede recusou o envio. Confira o saldo e o endereço.');
  },

  /** Depositar no cofre. onStep({step,n,total}) informa o andamento. Devolve o hash do depósito. */
  async deposit(amount, onStep) {
    await ensureSession();
    const from = await address();
    if (!from) throw new Error('Carteira não encontrada. Entre de novo.');
    const p = await api.portfolio(from);
    return runDeposit({ sendTx: (to, d, m) => sendTx(from, to, d, m), confirmTx, usdc: USDC, vault: VAULT.address, from, amount, p, onStep });
  },

  /** Resgatar do cofre para a carteira. all=true devolve tudo (redeem das cotas). */
  async withdraw(amount, all, onStep) {
    await ensureSession();
    const from = await address();
    if (!from) throw new Error('Carteira não encontrada. Entre de novo.');
    const p = await api.portfolio(from);
    return runWithdraw({ sendTx: (to, d, m) => sendTx(from, to, d, m), confirmTx, vault: VAULT.address, from, amount, all, p, onStep });
  },

  /** Taxa atual do cofre (fração ao ano, média de 7 dias) pela API REST da Morpho. Falha → null. */
  async apy() {
    const sel = net.chain.id + ':' + VAULT.address;
    for (const path of ['v0', 'v1']) {
      try {
        const x = parseApy(await getJSON(`https://api.morpho.org/${path}/vaults-v1/${sel}/apy-averages?lookback=seven_days`));
        if (x !== null) return x;
      } catch {}
    }
    return null;
  },
  /** Cotação do dólar em reais (aproximada). Falha → null. */
  async usdBrl() {
    try { return parseRate(await getJSON('https://economia.awesomeapi.com.br/json/last/USD-BRL')); } catch { return null; }
  },
  /** Últimas transferências de USDC da carteira (Blockscout). Lança erro se a consulta falhar. */
  async activity(addr) {
    const j = await getJSON(`https://base.blockscout.com/api/v2/addresses/${addr}/token-transfers?type=ERC-20`, 9000);
    return parseActivity(j, { me: addr, vault: VAULT.address, usdc: USDC });
  },

  /** Mostra o botão seguro da Coinbase que copia a chave privada para a área de transferência. */
  async exportKey(target, onStatus) {
    await ensureSession();
    const addr = await address();
    if (!addr) throw new Error('Carteira não encontrada.');
    const r = await createEvmKeyExportIframe({
      address: addr, target, projectId, label: 'Copiar chave privada', copiedLabel: 'Chave copiada',
      fullWidth: true, onStatusUpdate: onStatus,
      theme: {
        pageBg: '#14132C',
        buttonBg: '#2EE6C5', buttonBgHover: '#5AF0D6', buttonBgPressed: '#22C4A8', buttonBgFocus: '#2EE6C5',
        buttonBorder: '#2EE6C5', buttonBorderHover: '#5AF0D6', buttonBorderPressed: '#22C4A8',
        buttonBorderFocus: '#4A86FF', buttonBorderFocusInset: '#14132C',
        buttonText: '#0B0A1E', buttonTextHover: '#0B0A1E', buttonTextPressed: '#0B0A1E', buttonTextFocus: '#0B0A1E',
        buttonBorderRadius: 18, buttonFontSize: 16, buttonFontWeight: 700, buttonSize: 'lg',
      },
    });
    if (r.iframe) { r.iframe.style.width = '100%'; r.iframe.style.colorScheme = 'dark'; }
    return r;
  },

  ensureSession,
  signOut,
};

window.flexWallet = api;
export default api;

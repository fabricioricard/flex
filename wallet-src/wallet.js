// Módulo da carteira do Flex (rede de teste Base Sepolia).
// Empacotado por build.mjs em ../public/wallet.js e carregado só quando a pessoa abre a aba Carteira.
import {
  initialize, signInWithEmail, verifyEmailOTP, getCurrentUser, isSignedIn,
  sendEvmTransaction, signOut, createEvmKeyExportIframe,
} from '@coinbase/cdp-core';
import {
  createPublicClient, http, encodeFunctionData, parseUnits, formatUnits, formatEther, isAddress,
} from 'viem';
import { baseSepolia } from 'viem/chains';

const USDC = '0x036CbD53842c5426634e7929541eC2318f3dCF7e'; // USDC de teste (Circle) na Base Sepolia
const ERC20 = [
  { type: 'function', name: 'balanceOf', stateMutability: 'view', inputs: [{ name: 'a', type: 'address' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'transfer', stateMutability: 'nonpayable', inputs: [{ name: 'to', type: 'address' }, { name: 'v', type: 'uint256' }], outputs: [{ type: 'bool' }] },
];

const client = createPublicClient({ chain: baseSepolia, transport: http('https://sepolia.base.org') });
let projectId = null;
let flowId = null;

async function address() {
  const u = await getCurrentUser();
  return u?.evmAccountObjects?.[0]?.address || null;
}

const api = {
  chain: { name: baseSepolia.name, id: baseSepolia.id, explorer: 'https://sepolia.basescan.org' },
  usdc: USDC,

  async init(id) {
    projectId = id;
    await initialize({ projectId: id, disableAnalytics: true, ethereum: { createOnLogin: 'eoa' } });
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
    // a carteira pode levar um instante para aparecer logo após a criação
    for (let i = 0; i < 8; i++) {
      const a = await address();
      if (a) return a;
      await new Promise((r) => setTimeout(r, 500));
    }
    throw new Error('Carteira ainda sendo criada. Recarregue em alguns segundos.');
  },
  address,

  async balances(addr) {
    const [eth, usdc] = await Promise.all([
      client.getBalance({ address: addr }),
      client.readContract({ address: USDC, abi: ERC20, functionName: 'balanceOf', args: [addr] }),
    ]);
    return { eth: formatEther(eth), ethWei: eth, usdc: formatUnits(usdc, 6), usdcUnits: usdc };
  },

  /** Valida destino e valor antes de enviar. Devolve mensagem de erro ou null. */
  validateSend(to, amount, usdcUnits) {
    if (!isAddress(to)) return 'Endereço de destino inválido.';
    let v;
    try { v = parseUnits(String(amount).replace(',', '.'), 6); } catch { return 'Valor inválido.'; }
    if (v <= 0n) return 'Digite um valor maior que zero.';
    if (usdcUnits !== undefined && v > usdcUnits) return 'Saldo de USDC insuficiente.';
    return null;
  },

  /** Envia USDC (ERC-20) da carteira da pessoa. Ela paga a taxa em ETH de teste. */
  async sendUsdc(to, amount) {
    const from = await address();
    if (!from) throw new Error('Carteira não encontrada. Entre de novo.');
    const value = parseUnits(String(amount).replace(',', '.'), 6);
    const data = encodeFunctionData({ abi: ERC20, functionName: 'transfer', args: [to, value] });
    const [nonce, gas, fees] = await Promise.all([
      client.getTransactionCount({ address: from, blockTag: 'pending' }),
      client.estimateGas({ account: from, to: USDC, data }),
      client.estimateFeesPerGas(),
    ]);
    const r = await sendEvmTransaction({
      evmAccount: from,
      network: 'base-sepolia',
      transaction: {
        to: USDC, data, value: 0n, nonce,
        gas: (gas * 12n) / 10n,
        maxFeePerGas: fees.maxFeePerGas,
        maxPriorityFeePerGas: fees.maxPriorityFeePerGas,
        chainId: baseSepolia.id, type: 'eip1559',
      },
    });
    return r.transactionHash;
  },

  /** Mostra o botão seguro da Coinbase que copia a chave privada para a área de transferência. */
  async exportKey(target, onStatus) {
    const addr = await address();
    if (!addr) throw new Error('Carteira não encontrada.');
    return createEvmKeyExportIframe({
      address: addr, target, projectId, label: 'Copiar chave privada', onStatusUpdate: onStatus,
    });
  },

  signOut,
};

window.flexWallet = api;
export default api;

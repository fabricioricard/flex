// Lógica do cofre (ERC-4626) e dos dados lidos da blockchain. Sem dependência do SDK da Coinbase,
// para poder ser testada em Node com uma rede simulada (ver test-earn.mjs).
import { encodeFunctionData, parseUnits, formatUnits } from 'viem';

export const ERC20 = [
  { type: 'function', name: 'balanceOf', stateMutability: 'view', inputs: [{ name: 'a', type: 'address' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'allowance', stateMutability: 'view', inputs: [{ name: 'o', type: 'address' }, { name: 's', type: 'address' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'approve', stateMutability: 'nonpayable', inputs: [{ name: 's', type: 'address' }, { name: 'v', type: 'uint256' }], outputs: [{ type: 'bool' }] },
  { type: 'function', name: 'transfer', stateMutability: 'nonpayable', inputs: [{ name: 'to', type: 'address' }, { name: 'v', type: 'uint256' }], outputs: [{ type: 'bool' }] },
];

export const VAULT_ABI = [
  { type: 'function', name: 'balanceOf', stateMutability: 'view', inputs: [{ name: 'a', type: 'address' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'convertToAssets', stateMutability: 'view', inputs: [{ name: 'shares', type: 'uint256' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'maxWithdraw', stateMutability: 'view', inputs: [{ name: 'owner', type: 'address' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'maxRedeem', stateMutability: 'view', inputs: [{ name: 'owner', type: 'address' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'maxDeposit', stateMutability: 'view', inputs: [{ name: 'receiver', type: 'address' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'deposit', stateMutability: 'nonpayable', inputs: [{ name: 'assets', type: 'uint256' }, { name: 'receiver', type: 'address' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'withdraw', stateMutability: 'nonpayable', inputs: [{ name: 'assets', type: 'uint256' }, { name: 'receiver', type: 'address' }, { name: 'owner', type: 'address' }], outputs: [{ type: 'uint256' }] },
  { type: 'function', name: 'redeem', stateMutability: 'nonpayable', inputs: [{ name: 'shares', type: 'uint256' }, { name: 'receiver', type: 'address' }, { name: 'owner', type: 'address' }], outputs: [{ type: 'uint256' }] },
];

/** "1,5" ou "1.5" → unidades (6 casas). Devolve null se inválido ou com mais de 6 casas. */
export function parseAmount(a) {
  let s = String(a ?? '').trim().replace(',', '.');
  if (s.startsWith('.')) s = '0' + s;
  if (s.endsWith('.')) s = s.slice(0, -1);
  if (!/^\d+(\.\d{1,6})?$/.test(s)) return null;
  return parseUnits(s, 6);
}

export const fmtU = (x) =>
  Number(formatUnits(x, 6)).toLocaleString('pt-BR', { minimumFractionDigits: 2, maximumFractionDigits: 6 });

const NEED_ETH = 'Você precisa de um pouco de ETH na rede Base para pagar as taxas. Veja “Depositar”.';

export function validateDeposit(amount, p) {
  const v = parseAmount(amount);
  if (v === null) return 'Valor inválido. Use no máximo 6 casas decimais.';
  if (v <= 0n) return 'Digite um valor maior que zero.';
  if (v > p.usdcUnits) return 'Saldo de USDC insuficiente na carteira.';
  if (p.ethWei === 0n) return NEED_ETH;
  if (p.maxDepositUnits !== null && v > p.maxDepositUnits) return 'O cofre não aceita esse valor agora (limite de capacidade).';
  return null;
}

export function validateWithdraw(amount, all, p) {
  if (p.sharesUnits === 0n) return 'Você não tem saldo no cofre.';
  if (p.ethWei === 0n) return NEED_ETH;
  if (all) {
    if (p.maxRedeemUnits !== null && p.maxRedeemUnits > 0n && p.sharesUnits > p.maxRedeemUnits)
      return 'O cofre só consegue devolver parte do saldo agora. Resgate um valor menor.';
    return null;
  }
  const v = parseAmount(amount);
  if (v === null) return 'Valor inválido. Use no máximo 6 casas decimais.';
  if (v <= 0n) return 'Digite um valor maior que zero.';
  if (v > p.assetsUnits) return 'Você tem só ' + fmtU(p.assetsUnits) + ' USDC no cofre.';
  if (p.maxWithdrawUnits !== null && p.maxWithdrawUnits > 0n && v > p.maxWithdrawUnits)
    return 'O cofre só consegue devolver ' + fmtU(p.maxWithdrawUnits) + ' USDC agora.';
  return null;
}

/**
 * Depositar no cofre. Se o cofre ainda não tem autorização suficiente, autoriza o valor EXATO
 * (não ilimitado), espera confirmar e só então deposita. Se a autorização falhar, nada é depositado.
 */
export async function runDeposit({ sendTx, confirmTx, usdc, vault, from, amount, p, onStep = () => {} }) {
  const bad = validateDeposit(amount, p);
  if (bad) throw new Error(bad);
  const v = parseAmount(amount);
  const needApprove = p.allowanceUnits < v;
  const total = needApprove ? 2 : 1;
  if (needApprove) {
    onStep({ step: 'approve', n: 1, total });
    const h = await sendTx(usdc, encodeFunctionData({ abi: ERC20, functionName: 'approve', args: [vault, v] }),
      'A rede recusou a autorização do USDC. Nada foi movido.');
    onStep({ step: 'wait', n: 1, total });
    await confirmTx(h);
  }
  onStep({ step: 'deposit', n: total, total });
  const h = await sendTx(vault, encodeFunctionData({ abi: VAULT_ABI, functionName: 'deposit', args: [v, from] }),
    'O cofre recusou o depósito agora. Seu USDC continua na carteira.');
  onStep({ step: 'wait', n: total, total });
  await confirmTx(h);
  return h;
}

/** Resgatar do cofre para a carteira. "Tudo" usa redeem das cotas (não deixa resíduo). */
export async function runWithdraw({ sendTx, confirmTx, vault, from, amount, all, p, onStep = () => {} }) {
  const bad = validateWithdraw(amount, all, p);
  if (bad) throw new Error(bad);
  const data = all
    ? encodeFunctionData({ abi: VAULT_ABI, functionName: 'redeem', args: [p.sharesUnits, from, from] })
    : encodeFunctionData({ abi: VAULT_ABI, functionName: 'withdraw', args: [parseAmount(amount), from, from] });
  onStep({ step: 'withdraw', n: 1, total: 1 });
  const h = await sendTx(vault, data,
    'O cofre não conseguiu devolver esse valor agora (falta de liquidez). Tente um valor menor ou mais tarde.');
  onStep({ step: 'wait', n: 1, total: 1 });
  await confirmTx(h);
  return h;
}

/* ---------- leitura de dados externos (melhor esforço; falha → null) ---------- */

/** Resposta da API REST da Morpho (apy-averages) → fração ao ano (0.034 = 3,4%) ou null. */
export function parseApy(j) {
  let x = j?.data?.apy ?? j?.apy;
  if (typeof x !== 'number' || !Number.isFinite(x) || x < 0) return null;
  if (x > 1) x = x / 100; // se vier em %
  return x < 0.5 ? x : null;
}

/** AwesomeAPI USD-BRL → cotação ou null. */
export function parseRate(j) {
  const x = parseFloat(j?.USDBRL?.bid);
  return x > 1 && x < 20 ? x : null;
}

const HEX_ADDR = /^0x[0-9a-fA-F]{40}$/;
const HEX_HASH = /^0x[0-9a-fA-F]{64}$/;

/** Transferências de USDC (Blockscout v2) → lista para o histórico. */
export function parseActivity(j, { me, vault, usdc }) {
  const lo = (s) => String(s || '').toLowerCase();
  const items = Array.isArray(j?.items) ? j.items : [];
  const out = [];
  for (const x of items) {
    const tokenAddr = lo(x?.token?.address_hash ?? x?.token?.address);
    if (tokenAddr !== lo(usdc)) continue;
    const from = lo(x?.from?.hash), to = lo(x?.to?.hash);
    const hash = x?.transaction_hash ?? x?.tx_hash;
    const raw = x?.total?.value;
    const dec = Number(x?.total?.decimals ?? x?.token?.decimals ?? 6);
    if (!HEX_HASH.test(hash || '') || raw === undefined || !Number.isFinite(dec)) continue;
    const amount = Number(raw) / 10 ** dec;
    let kind, other = '';
    if (to === lo(vault)) kind = 'vault-in';
    else if (from === lo(vault)) kind = 'vault-out';
    else if (to === lo(me)) { kind = 'in'; other = from; }
    else if (from === lo(me)) { kind = 'out'; other = to; }
    else continue;
    const ts = Date.parse(x?.timestamp);
    out.push({ kind, amount, ts: Number.isFinite(ts) ? ts : 0, hash, other: HEX_ADDR.test(other) ? other : '' });
  }
  return out.sort((a, b) => b.ts - a.ts);
}

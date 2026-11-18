// Testes da lógica do cofre com uma rede simulada. Rodar: node test-earn.mjs
import assert from 'node:assert/strict';
import { decodeFunctionData, toFunctionSelector } from 'viem';
import {
  ERC20, VAULT_ABI, parseAmount, validateDeposit, validateWithdraw, runDeposit, runWithdraw,
  parseApy, parseRate, parseActivity,
} from './earn.js';

const USDC = '0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913';
const VAULT = '0xbeeF010f9cb27031ad51e3333f9aF9C6B1228183';
const ME = '0x1cccABf5818718d90bf31ad5A60F677971c96614';
let ok = 0; const t = (name, fn) => (async () => fn())().then(() => { ok++; console.log('ok  ', name); }, (e) => { console.error('FALHOU', name, '\n', e); process.exitCode = 1; });

const P = (o = {}) => ({
  usdcUnits: 10_000_000n, allowanceUnits: 0n, ethWei: 400000000000000n,
  sharesUnits: 5_000_000_000_000_000_000n, assetsUnits: 5_100_000n,
  maxWithdrawUnits: 5_100_000n, maxRedeemUnits: 5_000_000_000_000_000_000n, maxDepositUnits: 2n ** 256n - 1n, ...o,
});
function fakes({ failApprove = false, failDeposit = false, failConfirm = null } = {}) {
  const calls = [];
  return {
    calls,
    sendTx: async (to, data, msg) => {
      const fn = [...ERC20, ...VAULT_ABI].map((a) => a).find((a) => data.startsWith(toFunctionSelector(a)));
      calls.push({ to, name: fn.name, data, msg });
      if (fn.name === 'approve' && failApprove) throw new Error(msg);
      if (fn.name === 'deposit' && failDeposit) throw new Error(msg);
      return '0x' + String(calls.length).padStart(64, '0');
    },
    confirmTx: async (h) => { calls.push({ confirm: h }); if (failConfirm === calls.length) throw new Error('falhou na rede'); },
  };
}

await t('selectores ERC-20/ERC-4626 corretos', () => {
  const sel = (n, abi) => toFunctionSelector(abi.find((a) => a.name === n));
  assert.equal(sel('approve', ERC20), '0x095ea7b3');
  assert.equal(sel('transfer', ERC20), '0xa9059cbb');
  assert.equal(sel('deposit', VAULT_ABI), '0x6e553f65');
  assert.equal(sel('withdraw', VAULT_ABI), '0xb460af94');
  assert.equal(sel('redeem', VAULT_ABI), '0xba087652');
});

await t('parseAmount: estrito', () => {
  assert.equal(parseAmount('1,5'), 1_500_000n); assert.equal(parseAmount('0.000001'), 1n);
  assert.equal(parseAmount('.5'), 500_000n); assert.equal(parseAmount('5.'), 5_000_000n);
  for (const bad of ['', 'abc', '1.2.3', '-1', '1e3', '1,0000001', '0x10', ' ']) assert.equal(parseAmount(bad), null, bad);
});

await t('depósito com allowance insuficiente: approve EXATO → confirma → deposit', async () => {
  const f = fakes(); const steps = [];
  const h = await runDeposit({ ...f, usdc: USDC, vault: VAULT, from: ME, amount: '3,5', p: P(), onStep: (s) => steps.push(s.step + ':' + s.n + '/' + s.total) });
  const seq = f.calls.map((c) => c.name || 'confirm');
  assert.deepEqual(seq, ['approve', 'confirm', 'deposit', 'confirm']);
  const ap = decodeFunctionData({ abi: ERC20, data: f.calls[0].data });
  assert.equal(ap.args[0], VAULT); assert.equal(ap.args[1], 3_500_000n, 'approve do valor exato, não ilimitado');
  assert.equal(f.calls[0].to, USDC);
  const dp = decodeFunctionData({ abi: VAULT_ABI, data: f.calls[2].data });
  assert.equal(f.calls[2].to, VAULT); assert.deepEqual(dp.args, [3_500_000n, ME]);
  assert.deepEqual(steps, ['approve:1/2', 'wait:1/2', 'deposit:2/2', 'wait:2/2']);
  assert.match(h, /^0x/);
});

await t('depósito com allowance suficiente: só deposit', async () => {
  const f = fakes();
  await runDeposit({ ...f, usdc: USDC, vault: VAULT, from: ME, amount: '2', p: P({ allowanceUnits: 2_000_000n }) });
  assert.deepEqual(f.calls.map((c) => c.name || 'confirm'), ['deposit', 'confirm']);
});

await t('approve falha → NÃO deposita', async () => {
  const f = fakes({ failApprove: true });
  await assert.rejects(runDeposit({ ...f, usdc: USDC, vault: VAULT, from: ME, amount: '1', p: P() }), /autorização/);
  assert.deepEqual(f.calls.map((c) => c.name || 'confirm'), ['approve']);
});

await t('approve não confirma na rede → NÃO deposita', async () => {
  const f = fakes({ failConfirm: 2 });
  await assert.rejects(runDeposit({ ...f, usdc: USDC, vault: VAULT, from: ME, amount: '1', p: P() }), /falhou na rede/);
  assert.deepEqual(f.calls.map((c) => c.name || 'confirm'), ['approve', 'confirm']);
});

await t('validações de depósito', () => {
  assert.match(validateDeposit('0', P()), /maior que zero/);
  assert.match(validateDeposit('abc', P()), /inválido/);
  assert.match(validateDeposit('10,000001', P()), /insuficiente/);
  assert.match(validateDeposit('1', P({ ethWei: 0n })), /ETH/);
  assert.match(validateDeposit('5', P({ maxDepositUnits: 1_000_000n })), /capacidade/);
  assert.equal(validateDeposit('10', P()), null);
  assert.equal(validateDeposit('1', P({ maxDepositUnits: null })), null);
});

await t('resgate parcial usa withdraw(valor, eu, eu)', async () => {
  const f = fakes();
  await runWithdraw({ ...f, vault: VAULT, from: ME, amount: '2,25', all: false, p: P() });
  assert.deepEqual(f.calls.map((c) => c.name || 'confirm'), ['withdraw', 'confirm']);
  const d = decodeFunctionData({ abi: VAULT_ABI, data: f.calls[0].data });
  assert.deepEqual(d.args, [2_250_000n, ME, ME]); assert.equal(f.calls[0].to, VAULT);
});

await t('resgate total usa redeem(todas as cotas, eu, eu)', async () => {
  const f = fakes(); const p = P();
  await runWithdraw({ ...f, vault: VAULT, from: ME, amount: '', all: true, p });
  const d = decodeFunctionData({ abi: VAULT_ABI, data: f.calls[0].data });
  assert.equal(d.functionName, 'redeem'); assert.deepEqual(d.args, [p.sharesUnits, ME, ME]);
});

await t('validações de resgate', () => {
  assert.match(validateWithdraw('1', false, P({ sharesUnits: 0n })), /não tem saldo/);
  assert.match(validateWithdraw('9', false, P()), /tem só/);
  assert.match(validateWithdraw('4', false, P({ maxWithdrawUnits: 3_000_000n })), /só consegue devolver 3,00/);
  assert.match(validateWithdraw('', true, P({ maxRedeemUnits: 1n })), /parte do saldo/);
  assert.match(validateWithdraw('1', false, P({ ethWei: 0n })), /ETH/);
  assert.equal(validateWithdraw('', true, P()), null);
  assert.equal(validateWithdraw('1', false, P({ maxWithdrawUnits: 0n })), null, 'max=0 (V2 ou sem liquidez): deixa a simulação da rede decidir');
});

await t('resgate sem liquidez: erro claro e nada confirmado', async () => {
  const f = fakes();
  f.sendTx = async (_t, _d, msg) => { throw new Error(msg); };
  await assert.rejects(runWithdraw({ ...f, vault: VAULT, from: ME, amount: '1', all: false, p: P() }), /liquidez/);
});

await t('parseApy / parseRate', () => {
  assert.equal(parseApy({ data: { apy: 0.0336 } }), 0.0336); assert.equal(parseApy({ apy: 3.36 }), 0.0336);
  for (const j of [null, {}, { data: { apy: null } }, { data: { apy: -1 } }, { data: { apy: 900 } }, { data: { apy: 'x' } }]) assert.equal(parseApy(j), null);
  assert.equal(parseRate({ USDBRL: { bid: '5.31' } }), 5.31); assert.equal(parseRate({}), null); assert.equal(parseRate({ USDBRL: { bid: '0' } }), null);
});

await t('parseActivity classifica cofre/envio/recebimento e ignora lixo', () => {
  const H = (n) => '0x' + String(n).padStart(64, 'a');
  const other = '0x00000000000000000000000000000000000000AA';
  const mk = (from, to, v, n, ts, tok = USDC) => ({ from: { hash: from }, to: { hash: to }, token: { address_hash: tok, symbol: 'USDC' }, total: { value: String(v), decimals: '6' }, transaction_hash: H(n), timestamp: ts });
  const rows = parseActivity({ items: [
    mk(ME, VAULT, 3_000_000, 1, '2026-11-16T10:00:00Z'),
    mk(VAULT, ME, 1_500_000, 2, '2026-11-16T12:00:00Z'),
    mk(other, ME, 2_000_000, 3, '2026-11-15T10:00:00Z'),
    mk(ME, other, 500_000, 4, '2026-11-14T10:00:00Z'),
    mk(ME, other, 1, 5, '2026-11-14T10:00:00Z', '0x0000000000000000000000000000000000000001'),
    { ...mk(ME, other, 1, 6, 'x'), transaction_hash: 'não-é-hash' },
    mk(other, other, 9, 7, '2026-11-14T10:00:00Z'),
  ] }, { me: ME, vault: VAULT, usdc: USDC });
  assert.deepEqual(rows.map((r) => r.kind), ['vault-out', 'vault-in', 'in', 'out']);
  assert.equal(rows[0].amount, 1.5); assert.equal(rows[2].other, other.toLowerCase());
  assert.deepEqual(parseActivity(null, { me: ME, vault: VAULT, usdc: USDC }), []);
});

console.log(process.exitCode ? '\nHÁ FALHAS' : `\n${ok} testes passaram`);

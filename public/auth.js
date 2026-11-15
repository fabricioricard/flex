// Login com Firebase Auth (e-mail/senha e Google). Carregado como módulo ES.
import { firebaseConfig } from './firebase-config.js';

const SDK = 'https://www.gstatic.com/firebasejs/10.14.1/';

const $ = (id) => document.getElementById(id);
const configured = firebaseConfig.apiKey && !String(firebaseConfig.apiKey).startsWith('COLE');

const MSG = {
  'auth/invalid-credential': 'E-mail ou senha incorretos.',
  'auth/wrong-password': 'E-mail ou senha incorretos.',
  'auth/user-not-found': 'E-mail ou senha incorretos.',
  'auth/invalid-email': 'E-mail inválido.',
  'auth/email-already-in-use': 'Este e-mail já tem conta. Use Entrar.',
  'auth/weak-password': 'Senha fraca: use pelo menos 6 caracteres.',
  'auth/missing-password': 'Informe a senha.',
  'auth/too-many-requests': 'Muitas tentativas. Aguarde um pouco e tente de novo.',
  'auth/network-request-failed': 'Sem conexão. Verifique a internet.',
  'auth/popup-closed-by-user': '',
  'auth/cancelled-popup-request': '',
  'auth/popup-blocked': 'O navegador bloqueou a janela do Google. Libere pop-ups e tente de novo.',
  'auth/unauthorized-domain': 'Domínio não autorizado no Firebase (Authentication > Settings > Authorized domains).',
  'auth/operation-not-allowed': 'Método de login não ativado no Firebase.',
};

async function main() {
  // O SDK só é baixado quando o Firebase está configurado.
  const { initializeApp } = await import(SDK + 'firebase-app.js');
  const {
    getAuth, onAuthStateChanged, signInWithEmailAndPassword, createUserWithEmailAndPassword,
    signInWithPopup, GoogleAuthProvider, signOut, sendPasswordResetEmail,
  } = await import(SDK + 'firebase-auth.js');
  const app = initializeApp(firebaseConfig);
  const auth = getAuth(app);
  auth.languageCode = 'pt-BR';

  window.getToken = async () => (auth.currentUser ? auth.currentUser.getIdToken() : null);
  window.signOutNow = () => signOut(auth);

  const say = (text, ok) => {
    const m = $('aMsg');
    m.className = 'msg ' + (ok ? 'ok' : 'err');
    m.textContent = text;
  };
  const busy = (on) => document.querySelectorAll('#v-auth button').forEach((b) => (b.disabled = on));
  const run = async (fn) => {
    say('', true);
    busy(true);
    try { await fn(); }
    catch (e) { say(MSG[e.code] !== undefined ? MSG[e.code] : 'Não foi possível entrar. Tente novamente.', false); }
    busy(false);
  };
  const creds = () => [$('aEmail').value.trim(), $('aPass').value];

  $('aIn').onclick = () => run(() => signInWithEmailAndPassword(auth, ...creds()));
  $('aUp').onclick = () => run(() => createUserWithEmailAndPassword(auth, ...creds()));
  $('aG').onclick = () => run(() => signInWithPopup(auth, new GoogleAuthProvider()));
  $('aReset').onclick = () => run(async () => {
    const [email] = creds();
    if (!email) { say('Digite seu e-mail acima para receber o link.', false); return; }
    await sendPasswordResetEmail(auth, email);
    say('Enviamos um link para redefinir a senha, se o e-mail tiver conta.', true);
  });
  $('out').onclick = () => signOut(auth);

  onAuthStateChanged(auth, (u) => {
    if (u) window.startApp(u.email || '');
    else window.showAuth();
  });
}

if (!configured) {
  // Sem Firebase configurado: abre o app direto (modo demo local; na Vercel a API vai recusar).
  window.startApp();
} else {
  main().catch(() => {
    const off = document.getElementById('off');
    off.textContent = 'Não foi possível carregar o login. Verifique a conexão e recarregue.';
    off.classList.remove('hide');
  });
}

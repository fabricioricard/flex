// O SDK da Coinbase puxa o zustand, que importa o React só para hooks que o Flex não usa.
const React = {
  useSyncExternalStore() { throw new Error('React não está disponível neste pacote.'); },
  useDebugValue() {},
};
export default React;
export const useSyncExternalStore = React.useSyncExternalStore;
export const useDebugValue = React.useDebugValue;

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@xterm/xterm/css/xterm.css';
import { takeGrantFromFragment } from './pairing/pairing';
import { App, type GrantHolder } from './screens/App';
import './styles/global.css';

// Read and strip the pairing fragment before anything renders. The grant
// lives only in this in-memory holder until it is redeemed.
const grant: GrantHolder = {
  value: window.location.pathname === '/pair'
    ? takeGrantFromFragment(window.location, window.history)
    : undefined,
};

const root = document.getElementById('root');

if (root === null) {
  throw new Error('Missing application root');
}

createRoot(root).render(
  <StrictMode>
    <App grant={grant} />
  </StrictMode>,
);

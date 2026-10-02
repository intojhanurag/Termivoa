import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App, takeStartupLinks } from './screens/App';
import { AppErrorBoundary } from './screens/chunks';
import './styles/global.css';

// Read and strip every URL fragment before anything renders. Grants live
// only in memory until they are redeemed.
const links = takeStartupLinks(window.location, window.history, window.location);

const root = document.getElementById('root');

if (root === null) {
  throw new Error('Missing application root');
}

createRoot(root).render(
  <StrictMode>
    <AppErrorBoundary>
      <App {...links} />
    </AppErrorBoundary>
  </StrictMode>,
);

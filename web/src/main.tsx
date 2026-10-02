import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App, takeStartupLinks } from './screens/App';
import { ErrorBoundary, ErrorScreen, errorText } from './screens/chunks';
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
    <ErrorBoundary fallback={(error) => <ErrorScreen text={errorText(error)} />}>
      <App {...links} />
    </ErrorBoundary>
  </StrictMode>,
);

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './screens/App';
import './styles/global.css';

const root = document.getElementById('root');

if (root === null) {
  throw new Error('Missing application root');
}

createRoot(root).render(
  <StrictMode>
    <App />
  </StrictMode>,
);

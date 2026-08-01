import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './index.css';
import GraphExplorer from './GraphExplorer';

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <GraphExplorer />
  </StrictMode>,
);

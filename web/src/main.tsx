import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App.tsx";
import { ErrorBoundary } from "./shell/ErrorBoundary";

const container = document.getElementById("root");
if (!container) {
  throw new Error("missing #root element");
}

createRoot(container).render(
  <StrictMode>
    {/*
     * The outer boundary. React unmounts the entire root on an uncaught
     * render error, so without one here a single bad value anywhere above the
     * shell leaves a white page — on a wall-mounted screen, indistinguishable
     * from the machine being off. A boundary inside the shell handles the
     * common case with the chrome intact; this one is what is left when even
     * the shell cannot render.
     */}
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </StrictMode>,
);

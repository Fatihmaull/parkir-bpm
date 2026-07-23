import ReactDOM from "react-dom/client";
import App from "./App";
import "./styles.css";

// StrictMode sengaja tidak dipakai: double-mount dev-nya membuka 2 koneksi WS
// sehingga log event tampak ganda. Produksi tidak terpengaruh.
ReactDOM.createRoot(document.getElementById("root")!).render(<App />);

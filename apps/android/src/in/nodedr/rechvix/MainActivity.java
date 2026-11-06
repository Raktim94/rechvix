package in.nodedr.rechvix;

import android.Manifest;
import android.app.Activity;
import android.app.DownloadManager;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.graphics.Typeface;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.text.InputType;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputMethodManager;
import android.webkit.CookieManager;
import android.webkit.PermissionRequest;
import android.webkit.URLUtil;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.ScrollView;
import android.widget.TextView;

import java.net.HttpURLConnection;
import java.net.URL;

/**
 * Rechvix for Android: a thin, native shell around a self-hosted Rechvix server.
 *
 * Rechvix is a server product (Go API + PostgreSQL + the web app), so the
 * phone needs to know where the owner's server lives. First launch asks for it
 * once, checks /health/ready, remembers it, and from then on opens straight
 * into the app. The web app already has a phone layout (bottom bar, stacked
 * lists), so this shell adds only what a browser tab can't: a real app icon,
 * file/camera access for bill scanning and imports, downloads (PDF, exports),
 * and hand-off of tel:, mailto: and WhatsApp links.
 */
public class MainActivity extends Activity {
    private static final int EMERALD = 0xFF0F6E5C, INK = 0xFF16211C, MUTED = 0xFF5B6B62, PAPER = 0xFFF6F7F5, DANGER = 0xFF9C3A26;
    private static final int REQ_FILE = 11, REQ_CAMERA = 12;

    private SharedPreferences prefs;
    private FrameLayout root;
    private WebView web;
    private View connectView, errorView;
    private ProgressBar bar;
    private String server;
    private ValueCallback<Uri[]> filePicker;
    private PermissionRequest pendingPermission;

    @Override
    protected void onCreate(Bundle b) {
        super.onCreate(b);
        prefs = getSharedPreferences("rechvix", MODE_PRIVATE);
        root = new FrameLayout(this);
        root.setBackgroundColor(PAPER);
        setContentView(root);
        server = prefs.getString("server", null);
        if (server == null) showConnect(null); else openApp(server);
    }

    private int dp(int v) { return Math.round(v * getResources().getDisplayMetrics().density); }

    private TextView text(String s, int sp, int color, boolean bold) {
        TextView t = new TextView(this);
        t.setText(s);
        t.setTextSize(sp);
        t.setTextColor(color);
        if (bold) t.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        return t;
    }

    // ---------- connect screen ----------

    private void showConnect(String error) {
        if (connectView != null) root.removeView(connectView);
        hide(errorView);
        if (web != null) web.setVisibility(View.GONE);

        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        col.setPadding(dp(24), dp(56), dp(24), dp(24));

        ImageView logo = new ImageView(this);
        logo.setImageResource(R.mipmap.ic_launcher);
        col.addView(logo, new LinearLayout.LayoutParams(dp(56), dp(56)));

        TextView h = text("Connect to your Rechvix", 26, INK, true);
        h.setPadding(0, dp(28), 0, dp(8));
        col.addView(h);
        TextView p = text("Enter the address you use to open Rechvix in a browser. You only do this once.", 15, MUTED, false);
        p.setLineSpacing(0, 1.2f);
        col.addView(p);

        TextView label = text("Server address", 13, INK, true);
        label.setPadding(0, dp(28), 0, dp(6));
        col.addView(label);

        final EditText url = new EditText(this);
        url.setHint("books.yourshop.com");
        url.setSingleLine(true);
        url.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        url.setImeOptions(EditorInfo.IME_ACTION_GO);
        url.setTextSize(17);
        url.setTextColor(INK);
        url.setHintTextColor(0xFF8A958E);
        url.setBackgroundResource(R.drawable.field);
        url.setPadding(dp(14), 0, dp(14), 0);
        url.setText(prefs.getString("server", ""));
        col.addView(url, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(52)));

        final TextView msg = text(error == null ? "" : error, 14, DANGER, false);
        msg.setPadding(0, dp(10), 0, 0);
        msg.setVisibility(error == null ? View.GONE : View.VISIBLE);
        col.addView(msg);

        final Button go = new Button(this);
        go.setText("Connect");
        go.setAllCaps(false);
        go.setTextSize(16);
        go.setTextColor(Color.WHITE);
        go.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        go.setBackgroundResource(R.drawable.button);
        go.setStateListAnimator(null);
        LinearLayout.LayoutParams gl = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(52));
        gl.topMargin = dp(18);
        col.addView(go, gl);

        TextView hint = text("On a shop network without a certificate, an address like 192.168.1.40:8080 works too.", 13, MUTED, false);
        hint.setPadding(0, dp(18), 0, 0);
        hint.setLineSpacing(0, 1.2f);
        col.addView(hint);

        final Runnable connect = () -> {
            String raw = url.getText().toString().trim();
            if (raw.isEmpty()) { msg.setText("Type your server address first."); msg.setVisibility(View.VISIBLE); return; }
            go.setEnabled(false);
            go.setText("Checking…");
            msg.setVisibility(View.GONE);
            InputMethodManager imm = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
            imm.hideSoftInputFromWindow(url.getWindowToken(), 0);
            final String normalized = normalize(raw);
            new Thread(() -> {
                final String problem = probe(normalized);
                runOnUiThread(() -> {
                    if (problem == null) {
                        prefs.edit().putString("server", normalized).apply();
                        server = normalized;
                        openApp(normalized);
                    } else {
                        go.setEnabled(true);
                        go.setText("Connect");
                        msg.setText(problem);
                        msg.setVisibility(View.VISIBLE);
                    }
                });
            }).start();
        };
        go.setOnClickListener(v -> connect.run());
        url.setOnEditorActionListener((v, a, e) -> { connect.run(); return true; });

        ScrollView sv = new ScrollView(this);
        sv.setFillViewport(true);
        sv.addView(col);
        connectView = sv;
        root.addView(sv, new FrameLayout.LayoutParams(-1, -1));
    }

    /** "shop.example.com" -> https://shop.example.com ; private/LAN addresses -> http. */
    static String normalize(String raw) {
        String s = raw.trim();
        if (!s.contains("://")) {
            String host = s.split("[/:]")[0];
            boolean lan = host.equals("localhost") || host.endsWith(".local") || host.matches("^(10|127)\\..*")
                    || host.matches("^192\\.168\\..*") || host.matches("^172\\.(1[6-9]|2\\d|3[01])\\..*");
            s = (lan ? "http://" : "https://") + s;
        }
        while (s.endsWith("/")) s = s.substring(0, s.length() - 1);
        return s;
    }

    /** Returns null when the server looks like Rechvix and is ready; otherwise a human message. */
    private String probe(String base) {
        try {
            HttpURLConnection c = (HttpURLConnection) new URL(base + "/health/ready").openConnection();
            c.setConnectTimeout(6000);
            c.setReadTimeout(6000);
            int code = c.getResponseCode();
            if (code == 200) return null;
            return "That address answered, but not like Rechvix (status " + code + "). Check it and try again.";
        } catch (javax.net.ssl.SSLException e) {
            return "The secure connection failed. Check the address, or the server's certificate.";
        } catch (java.net.UnknownHostException e) {
            return "Can't find that address. Check the spelling and your internet connection.";
        } catch (Exception e) {
            return "Can't reach the server. Check the address and that you are online.";
        }
    }

    // ---------- the app ----------

    private void openApp(String base) {
        if (connectView != null) { root.removeView(connectView); connectView = null; }
        hide(errorView);
        if (web == null) buildWebView();
        web.setVisibility(View.VISIBLE);
        web.loadUrl(base + "/");
    }

    private void buildWebView() {
        web = new WebView(this);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setAllowFileAccess(false);
        s.setUserAgentString(s.getUserAgentString() + " RechvixAndroid/0.1");
        CookieManager.getInstance().setAcceptCookie(true);
        CookieManager.getInstance().setAcceptThirdPartyCookies(web, false);

        web.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest r) {
                Uri u = r.getUrl();
                if (server != null && u.toString().startsWith(server)) return false;
                try { startActivity(new Intent(Intent.ACTION_VIEW, u)); } catch (Exception ignored) { }
                return true;
            }
            @Override public void onPageFinished(WebView v, String url) { bar.setVisibility(View.GONE); }
            @Override public void onReceivedError(WebView v, WebResourceRequest r, WebResourceError e) {
                if (r.isForMainFrame()) showError();
            }
        });
        web.setWebChromeClient(new WebChromeClient() {
            @Override public void onProgressChanged(WebView v, int p) {
                bar.setVisibility(p < 100 ? View.VISIBLE : View.GONE);
                bar.setProgress(p);
            }
            @Override public boolean onShowFileChooser(WebView v, ValueCallback<Uri[]> cb, FileChooserParams params) {
                if (filePicker != null) filePicker.onReceiveValue(null);
                filePicker = cb;
                try { startActivityForResult(params.createIntent(), REQ_FILE); }
                catch (Exception e) { filePicker = null; return false; }
                return true;
            }
            @Override public void onPermissionRequest(final PermissionRequest r) {
                // Bill scanning and barcode entry use the camera; ask the OS first.
                if (Build.VERSION.SDK_INT >= 23 && checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
                    pendingPermission = r;
                    requestPermissions(new String[]{Manifest.permission.CAMERA}, REQ_CAMERA);
                } else r.grant(r.getResources());
            }
        });
        web.setDownloadListener((url, ua, cd, mime, len) -> {
            try {
                DownloadManager.Request q = new DownloadManager.Request(Uri.parse(url));
                String name = URLUtil.guessFileName(url, cd, mime);
                q.setMimeType(mime);
                q.addRequestHeader("Cookie", CookieManager.getInstance().getCookie(url));
                q.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
                q.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
                ((DownloadManager) getSystemService(DOWNLOAD_SERVICE)).enqueue(q);
                android.widget.Toast.makeText(this, "Downloading " + name, android.widget.Toast.LENGTH_SHORT).show();
            } catch (Exception e) {
                try { startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(url))); } catch (Exception ignored) { }
            }
        });

        root.addView(web, new FrameLayout.LayoutParams(-1, -1));
        bar = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        bar.setMax(100);
        bar.getProgressDrawable().setColorFilter(EMERALD, android.graphics.PorterDuff.Mode.SRC_IN);
        root.addView(bar, new FrameLayout.LayoutParams(-1, dp(3)));
    }

    private void showError() {
        web.setVisibility(View.GONE);
        if (errorView != null) { errorView.setVisibility(View.VISIBLE); return; }
        LinearLayout col = new LinearLayout(this);
        col.setOrientation(LinearLayout.VERTICAL);
        col.setGravity(Gravity.CENTER_HORIZONTAL);
        col.setPadding(dp(28), dp(120), dp(28), dp(24));
        col.setBackgroundColor(PAPER);
        TextView h = text("Can't reach Rechvix", 22, INK, true);
        h.setGravity(Gravity.CENTER);
        col.addView(h);
        TextView p = text("Check your connection, or that the server is switched on. Your data is safe on the server.", 15, MUTED, false);
        p.setGravity(Gravity.CENTER);
        p.setPadding(0, dp(10), 0, dp(26));
        col.addView(p);
        Button retry = new Button(this);
        retry.setText("Try again");
        retry.setAllCaps(false);
        retry.setTextColor(Color.WHITE);
        retry.setBackgroundResource(R.drawable.button);
        retry.setStateListAnimator(null);
        retry.setOnClickListener(v -> { hide(errorView); web.setVisibility(View.VISIBLE); web.loadUrl(server + "/"); });
        col.addView(retry, new LinearLayout.LayoutParams(-1, dp(52)));
        Button change = new Button(this);
        change.setText("Change server");
        change.setAllCaps(false);
        change.setTextColor(INK);
        change.setBackgroundResource(R.drawable.button_quiet);
        change.setStateListAnimator(null);
        change.setOnClickListener(v -> showConnect(null));
        LinearLayout.LayoutParams cl = new LinearLayout.LayoutParams(-1, dp(52));
        cl.topMargin = dp(12);
        col.addView(change, cl);
        errorView = col;
        root.addView(col, new FrameLayout.LayoutParams(-1, -1));
    }

    private void hide(View v) { if (v != null) v.setVisibility(View.GONE); }

    // ---------- plumbing ----------

    @Override protected void onActivityResult(int req, int res, Intent data) {
        if (req == REQ_FILE && filePicker != null) {
            filePicker.onReceiveValue(res == RESULT_OK && data != null ? WebChromeClient.FileChooserParams.parseResult(res, data) : null);
            filePicker = null;
        }
    }

    @Override public void onRequestPermissionsResult(int req, String[] perms, int[] results) {
        if (req == REQ_CAMERA && pendingPermission != null) {
            if (results.length > 0 && results[0] == PackageManager.PERMISSION_GRANTED) pendingPermission.grant(pendingPermission.getResources());
            else pendingPermission.deny();
            pendingPermission = null;
        }
    }

    @Override public boolean onKeyDown(int code, KeyEvent e) {
        if (code == KeyEvent.KEYCODE_BACK && web != null && web.getVisibility() == View.VISIBLE && web.canGoBack()) {
            web.goBack();
            return true;
        }
        return super.onKeyDown(code, e);
    }
}

package ru.tvhub.remote;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.app.NotificationManager;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.provider.Settings;
import android.text.InputType;
import android.util.TypedValue;
import android.view.Gravity;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowManager;
import android.webkit.JavascriptInterface;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.URL;

import com.google.mlkit.vision.barcode.common.Barcode;
import com.google.mlkit.vision.codescanner.GmsBarcodeScannerOptions;
import com.google.mlkit.vision.codescanner.GmsBarcodeScanning;

/**
 * Пульт TVHub: открывает страницу пульта, которую показывает TVHub на компьютере.
 * Сам интерфейс пульта приходит с компьютера, поэтому обновляется вместе с TVHub.
 * Музыку компьютера показывает в шторке телефона (MusicService).
 */
public class MainActivity extends Activity {

    private static final int BG = Color.parseColor("#0A0C11");
    private static final int SURFACE = Color.parseColor("#161A23");
    private static final int TEXT = Color.parseColor("#F3F5F9");
    private static final int MUTED = Color.parseColor("#98A2B5");
    private static final int ACCENT = Color.parseColor("#4F8CFF");

    private SharedPreferences prefs;
    private FrameLayout root;
    private WebView web;
    private boolean pageOk = false;

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        prefs = getSharedPreferences("tvhub", MODE_PRIVATE);
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        root = new FrameLayout(this);
        root.setBackgroundColor(BG);
        setContentView(root);
        // Android 15 растягивает приложение под шторку и клавиатуру. Пока открыта клавиатура,
        // поджимаем страницу снизу, чтобы поле ввода и список не уезжали под неё и под шторку.
        if (Build.VERSION.SDK_INT >= 35) {
            root.setOnApplyWindowInsetsListener((v, insets) -> {
                int ime = insets.getInsets(android.view.WindowInsets.Type.ime()).bottom;
                v.setPadding(0, 0, 0, ime);
                return insets;
            });
        }
        String url = prefs.getString("url", "");
        if (url.isEmpty()) showSetup(null);
        else openRemote(url);
        checkUpdate(false);
        askNotifications();
    }

    /** Спросить разрешение ещё раз, а если Android больше не показывает вопрос — открыть настройки уведомлений. */
    private void askNotificationsAgain() {
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
                && (!prefs.getBoolean("askedNotify", false) || shouldShowRequestPermissionRationale(Manifest.permission.POST_NOTIFICATIONS))) {
            prefs.edit().putBoolean("askedNotify", true).apply();
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 1);
            return;
        }
        Intent i = new Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, getPackageName());
        try {
            startActivity(i);
        } catch (Exception e) {
            startActivity(new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:" + getPackageName())));
        }
    }

    @Override
    public void onRequestPermissionsResult(int code, String[] perms, int[] res) {
        super.onRequestPermissionsResult(code, perms, res);
        MusicService.stop(this); // перезапускаем, чтобы карточка появилась в шторке сразу
        MusicService.start(this);
    }

    /** Android 13+ спрашивает разрешение на уведомления — без него музыки в шторке не будет. Спрашиваем один раз. */
    private void askNotifications() {
        if (Build.VERSION.SDK_INT < 33 || prefs.getBoolean("askedNotify", false)) return;
        if (checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED) return;
        prefs.edit().putBoolean("askedNotify", true).apply();
        requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 1);
    }

    // ---------- Новые версии приложения ----------

    private static final String RELEASE = "https://github.com/" + BuildConfig.REPO + "/releases/download/remote-app/";

    /** Проверяет, вышла ли новая версия приложения (не чаще раза в 6 часов, если не просили вручную). */
    private void checkUpdate(boolean manual) {
        long last = prefs.getLong("updateChecked", 0);
        if (!manual && System.currentTimeMillis() - last < 6 * 3600_000L) return;
        new Thread(() -> {
            try {
                HttpURLConnection c = (HttpURLConnection) new URL(RELEASE + "version.json").openConnection();
                c.setConnectTimeout(8000);
                c.setReadTimeout(8000);
                c.setInstanceFollowRedirects(true);
                StringBuilder sb = new StringBuilder();
                try (BufferedReader r = new BufferedReader(new InputStreamReader(c.getInputStream(), "UTF-8"))) {
                    for (String l; (l = r.readLine()) != null; ) sb.append(l);
                }
                JSONObject v = new JSONObject(sb.toString());
                int code = v.getInt("versionCode");
                String name = v.optString("versionName", String.valueOf(code));
                prefs.edit().putLong("updateChecked", System.currentTimeMillis()).apply();
                runOnUiThread(() -> {
                    if (code > BuildConfig.VERSION_CODE) offerUpdate(name);
                    else if (manual) toastDialog("У вас последняя версия приложения (" + BuildConfig.VERSION_NAME + ").");
                });
            } catch (Exception e) {
                if (manual) runOnUiThread(() -> toastDialog("Не удалось проверить обновление. Проверьте интернет на телефоне."));
            }
        }).start();
    }

    private void toastDialog(String msg) {
        new AlertDialog.Builder(this).setMessage(msg).setPositiveButton("Хорошо", null).show();
    }

    private void offerUpdate(String version) {
        new AlertDialog.Builder(this)
                .setTitle("Новая версия приложения " + version)
                .setMessage("Скачать новую версию пульта? Сейчас у вас " + BuildConfig.VERSION_NAME + ".")
                .setPositiveButton("Скачать", (d, w) -> {
                    startActivity(new Intent(Intent.ACTION_VIEW, Uri.parse(RELEASE + "TVHub-Remote.apk")));
                    explainInstall();
                })
                .setNegativeButton("Позже", null)
                .show();
    }

    /** Новая версия подписана другим ключом, поэтому старую нужно удалить перед установкой. */
    private void explainInstall() {
        new AlertDialog.Builder(this)
                .setTitle("Как установить")
                .setMessage("1. Дождитесь, пока файл TVHub-Remote.apk скачается.\n\n"
                        + "2. Удалите эту версию приложения — кнопка ниже.\n\n"
                        + "3. Откройте TVHub-Remote.apk в «Загрузках» и установите.\n\n"
                        + "4. В новом приложении один раз отсканируйте QR-код пульта в TVHub.")
                .setPositiveButton("Удалить эту версию", (d, w) ->
                        startActivity(new Intent(Intent.ACTION_DELETE, Uri.parse("package:" + getPackageName()))))
                .setNegativeButton("Позже", null)
                .show();
    }

    // ---------- Подключение ----------

    private int dp(float v) {
        return (int) TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v, getResources().getDisplayMetrics());
    }

    private TextView text(String s, float size, int color, boolean bold) {
        TextView t = new TextView(this);
        t.setText(s);
        t.setTextSize(size);
        t.setTextColor(color);
        t.setGravity(Gravity.CENTER);
        if (bold) t.setTypeface(Typeface.DEFAULT_BOLD);
        return t;
    }

    private Button button(String s, boolean primary, View.OnClickListener l) {
        Button b = new Button(this);
        b.setText(s);
        b.setAllCaps(false);
        b.setTextSize(17);
        b.setTextColor(TEXT);
        GradientDrawable bg = new GradientDrawable();
        bg.setCornerRadius(dp(16));
        bg.setColor(primary ? ACCENT : SURFACE);
        b.setBackground(bg);
        b.setOnClickListener(l);
        LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(56));
        lp.topMargin = dp(12);
        b.setLayoutParams(lp);
        return b;
    }

    /** Экран первого запуска (или ошибки): сканировать QR-код с экрана TVHub. */
    private void showSetup(String error) {
        if (web != null) { web.destroy(); web = null; }
        root.removeAllViews();
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setGravity(Gravity.CENTER);
        box.setPadding(dp(28), dp(28), dp(28), dp(28));

        TextView logo = text("TV", 26, Color.WHITE, true);
        GradientDrawable lg = new GradientDrawable(GradientDrawable.Orientation.TL_BR, new int[]{ACCENT, Color.parseColor("#8B5CF6")});
        lg.setCornerRadius(dp(22));
        logo.setBackground(lg);
        LinearLayout.LayoutParams llp = new LinearLayout.LayoutParams(dp(84), dp(84));
        llp.gravity = Gravity.CENTER_HORIZONTAL;
        llp.bottomMargin = dp(20);
        logo.setLayoutParams(llp);
        box.addView(logo);

        box.addView(text("Пульт TVHub", 26, TEXT, true));
        TextView hint = text(error != null ? error
                : "На компьютере откройте TVHub → «Система» → «Пульт на телефоне» и отсканируйте QR-код.\n\nТелефон и компьютер должны быть в одной сети Wi-Fi.",
                15, error != null ? Color.parseColor("#FF8A8E") : MUTED, false);
        hint.setPadding(0, dp(12), 0, dp(16));
        hint.setLineSpacing(0, 1.25f);
        box.addView(hint);

        box.addView(button("Сканировать QR-код", true, v -> scan()));
        String saved = prefs.getString("url", "");
        if (error != null && !saved.isEmpty()) box.addView(button("Попробовать снова", false, v -> openRemote(saved)));
        box.addView(button("Ввести адрес вручную", false, v -> askUrl()));

        root.addView(box, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
    }

    private void scan() {
        GmsBarcodeScannerOptions opts = new GmsBarcodeScannerOptions.Builder()
                .setBarcodeFormats(Barcode.FORMAT_QR_CODE)
                .enableAutoZoom()
                .build();
        GmsBarcodeScanning.getClient(this, opts).startScan()
                .addOnSuccessListener(code -> useUrl(code.getRawValue()))
                .addOnFailureListener(e -> showSetup("Не удалось открыть сканер. Можно ввести адрес вручную — он написан под QR-кодом в TVHub."));
    }

    private void askUrl() {
        EditText in = new EditText(this);
        in.setHint("http://192.168.1.5:8777/r?k=…");
        in.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        in.setText(prefs.getString("url", ""));
        new AlertDialog.Builder(this)
                .setTitle("Адрес пульта")
                .setMessage("Он написан в TVHub под QR-кодом")
                .setView(in)
                .setPositiveButton("Подключить", (d, w) -> useUrl(in.getText().toString().trim()))
                .setNegativeButton("Отмена", null)
                .show();
    }

    private void useUrl(String raw) {
        if (raw == null) return;
        Uri u = Uri.parse(raw.trim());
        if (!"http".equals(u.getScheme()) || u.getHost() == null || !"/r".equals(u.getPath()) || u.getQueryParameter("k") == null) {
            showSetup("Это не QR-код пульта TVHub. Отсканируйте код на экране «Пульт на телефоне».");
            return;
        }
        prefs.edit().putString("url", u.toString()).apply();
        openRemote(u.toString());
        MusicService.start(this);
    }

    // ---------- Пульт ----------

    private void openRemote(String url) {
        root.removeAllViews();
        pageOk = false;
        web = new WebView(this);
        web.setBackgroundColor(BG);
        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setUserAgentString(s.getUserAgentString() + " TVHubRemoteApp/1");
        web.addJavascriptInterface(new Bridge(), "TVHubApp");
        final String host = Uri.parse(url).getHost();
        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest r) {
                return !host.equals(r.getUrl().getHost()); // чужие ссылки не открываем
            }

            @Override
            public void onPageFinished(WebView v, String u) { pageOk = true; }

            @Override
            public void onReceivedError(WebView v, WebResourceRequest r, WebResourceError e) {
                if (r.isForMainFrame()) {
                    showSetup("Не удалось подключиться к компьютеру.\n\nПроверьте, что TVHub запущен и телефон в той же сети Wi-Fi. Если компьютер получил новый адрес, отсканируйте QR-код заново.");
                }
            }
        });
        root.addView(web, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        web.loadUrl(url);
    }

    /** Кнопки громкости телефона управляют громкостью компьютера. */
    @Override
    public boolean dispatchKeyEvent(KeyEvent e) {
        int k = e.getKeyCode();
        if (web != null && pageOk && prefs.getBoolean("volumeKeys", true)
                && (k == KeyEvent.KEYCODE_VOLUME_UP || k == KeyEvent.KEYCODE_VOLUME_DOWN)) {
            if (e.getAction() == KeyEvent.ACTION_DOWN) {
                web.evaluateJavascript("sendKey('" + (k == KeyEvent.KEYCODE_VOLUME_UP ? "volup" : "voldown") + "')", null);
            }
            return true;
        }
        return super.dispatchKeyEvent(e);
    }

    @Override
    protected void onResume() {
        super.onResume();
        if (web != null) web.onResume();
        MusicService.start(this); // следим, не играет ли музыка на компьютере (для шторки)
    }

    @Override
    protected void onPause() {
        if (web != null) web.onPause();
        super.onPause();
    }

    /** Связь со страницей пульта: кнопка «Сменить компьютер» и настройка кнопок громкости. */
    private class Bridge {
        @JavascriptInterface
        public void rescan() {
            runOnUiThread(() -> showSetup(null));
        }

        @JavascriptInterface
        public void checkUpdate() {
            runOnUiThread(() -> MainActivity.this.checkUpdate(true));
        }

        @JavascriptInterface
        public String version() {
            return BuildConfig.VERSION_NAME;
        }

        @JavascriptInterface
        public boolean volumeKeys() {
            return prefs.getBoolean("volumeKeys", true);
        }

        @JavascriptInterface
        public void setVolumeKeys(boolean on) {
            prefs.edit().putBoolean("volumeKeys", on).apply();
        }

        @JavascriptInterface
        public boolean musicNotify() {
            return prefs.getBoolean("musicNotify", true);
        }

        @JavascriptInterface
        public void setMusicNotify(boolean on) {
            prefs.edit().putBoolean("musicNotify", on).apply();
            if (on) {
                runOnUiThread(() -> {
                    if (!notifyAllowed()) askNotificationsAgain();
                    MusicService.start(MainActivity.this);
                });
            } else MusicService.stop(MainActivity.this);
        }

        /** Разрешены ли приложению уведомления (без них музыки в шторке не будет). */
        @JavascriptInterface
        public boolean notifyAllowed() {
            NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
            if (nm != null && !nm.areNotificationsEnabled()) return false;
            return Build.VERSION.SDK_INT < 33 || checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED;
        }

        @JavascriptInterface
        public void allowNotify() {
            runOnUiThread(MainActivity.this::askNotificationsAgain);
        }
    }
}

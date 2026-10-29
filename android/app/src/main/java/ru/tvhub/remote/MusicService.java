package ru.tvhub.remote;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.ServiceInfo;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.drawable.Icon;
import android.media.MediaMetadata;
import android.media.session.MediaSession;
import android.media.session.PlaybackState;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.PowerManager;
import android.os.SystemClock;

import org.json.JSONObject;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

/**
 * Музыка компьютера в шторке телефона: что играет в Яндекс Музыке (или в любом другом окне),
 * обложка, пауза, треки, перемотка и «Мне нравится». Пока музыки нет, в шторке остаётся
 * тихая карточка с кнопкой «плей» — ею можно включить «Мою волну». Служба работает
 * «на переднем плане», иначе Android закрывает её через минуту после сворачивания приложения.
 * Если компьютер недоступен 10 минут — служба выключается до следующего открытия приложения.
 */
public class MusicService extends Service {

    private static final String CHANNEL = "music";
    private static final int NOTE_ID = 1;
    private static final long GIVE_UP_MS = 10 * 60_000;

    private final Handler main = new Handler(Looper.getMainLooper());
    private final ExecutorService io = Executors.newSingleThreadExecutor();
    private MediaSession session;
    private NotificationManager nm;
    private String base = "", key = "";

    // Что играет сейчас
    private String title = "", artist = "", album = "", coverKey = "";
    private boolean playing, shown, foreground, destroyed;
    private Boolean liked;
    private long durMs;
    private Bitmap cover;
    private long lastMusic, lastOk;

    /** Запускает слежение за музыкой, если пользователь его не выключил. */
    static void start(Context c) {
        SharedPreferences p = c.getSharedPreferences("tvhub", MODE_PRIVATE);
        if (!p.getBoolean("musicNotify", true) || p.getString("url", "").isEmpty()) return;
        try {
            c.startService(new Intent(c, MusicService.class));
        } catch (Exception ignored) {
            // Приложение в фоне — Android не даёт запустить службу; запустим при следующем открытии
        }
    }

    static void stop(Context c) {
        c.stopService(new Intent(c, MusicService.class));
    }

    @Override
    public IBinder onBind(Intent i) { return null; }

    @Override
    public void onCreate() {
        super.onCreate();
        nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (Build.VERSION.SDK_INT >= 26) {
            NotificationChannel ch = new NotificationChannel(CHANNEL, "Музыка на компьютере", NotificationManager.IMPORTANCE_LOW);
            ch.setDescription("Что играет на компьютере с TVHub и кнопки управления");
            ch.setShowBadge(false);
            nm.createNotificationChannel(ch);
        }
        session = new MediaSession(this, "TVHub");
        session.setCallback(new MediaSession.Callback() {
            @Override public void onPlay() { command("playpause", null); }
            @Override public void onPause() { command("playpause", null); }
            @Override public void onSkipToNext() { command("next", null); }
            @Override public void onSkipToPrevious() { command("prev", null); }
            @Override public void onSeekTo(long ms) { command("seek", ms / 1000.0); }
            @Override public void onCustomAction(String action, Bundle extras) { if ("like".equals(action)) command("like", null); }
        });
        session.setActive(true);
        lastMusic = lastOk = SystemClock.elapsedRealtime();
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        Uri u = Uri.parse(getSharedPreferences("tvhub", MODE_PRIVATE).getString("url", ""));
        if (u.getHost() == null || u.getQueryParameter("k") == null) {
            stopSelf();
            return START_NOT_STICKY;
        }
        base = u.getScheme() + "://" + u.getHost() + (u.getPort() > 0 ? ":" + u.getPort() : "");
        key = u.getQueryParameter("k");
        String a = intent != null ? intent.getAction() : null;
        if ("stop".equals(a)) {
            stopSelf();
            return START_NOT_STICKY;
        }
        if (a != null) command(a, null); // кнопка в уведомлении (на старых Android)
        // На передний план — сразу, пока приложение открыто: позже Android этого уже не разрешит
        if (!foreground) show();
        main.removeCallbacks(poll);
        main.post(poll);
        return START_NOT_STICKY;
    }

    @Override
    public void onDestroy() {
        destroyed = true;
        main.removeCallbacksAndMessages(null);
        io.shutdownNow();
        session.release();
        if (foreground) stopForeground(true);
        nm.cancel(NOTE_ID);
        super.onDestroy();
    }

    // ---------- Опрос компьютера ----------

    private final Runnable poll = new Runnable() {
        @Override
        public void run() {
            io.execute(() -> {
                JSONObject st = null;
                try {
                    st = new JSONObject(http("GET", base + "/r/api/media", null)).optJSONObject("media");
                } catch (Exception ignored) {
                }
                JSONObject s = st;
                Bitmap bmp = s != null ? loadCover(s) : null;
                main.post(() -> apply(s, bmp));
            });
            if (destroyed) return;
            PowerManager pm = (PowerManager) getSystemService(POWER_SERVICE);
            main.postDelayed(this, pm != null && pm.isInteractive() ? 2000 : 5000);
        }
    };

    private void apply(JSONObject st, Bitmap bmp) {
        if (destroyed) return;
        long now = SystemClock.elapsedRealtime();
        if (st != null) lastOk = now;
        String t = st != null ? st.optString("title", "") : "";
        if (!t.isEmpty()) lastMusic = now;
        if (now - lastOk > GIVE_UP_MS) {
            stopSelf(); // компьютер давно недоступен
            return;
        }
        if (st == null) return; // связь пропала ненадолго — оставляем как было
        if (t.isEmpty()) {
            idle();
            return;
        }
        String ar = st.optString("artist", ""), al = st.optString("album", "");
        long dur = (long) (st.optDouble("dur", 0) * 1000);
        boolean meta = !t.equals(title) || !ar.equals(artist) || dur != durMs || bmp != cover;
        title = t;
        artist = ar;
        album = al;
        durMs = dur;
        cover = bmp;
        playing = "Playing".equals(st.optString("status"));
        liked = st.has("liked") ? st.optBoolean("liked") : null;
        if (meta) {
            MediaMetadata.Builder m = new MediaMetadata.Builder()
                    .putString(MediaMetadata.METADATA_KEY_TITLE, title)
                    .putString(MediaMetadata.METADATA_KEY_ARTIST, artist)
                    .putString(MediaMetadata.METADATA_KEY_ALBUM, album)
                    .putLong(MediaMetadata.METADATA_KEY_DURATION, durMs);
            if (cover != null) m.putBitmap(MediaMetadata.METADATA_KEY_ALBUM_ART, cover);
            session.setMetadata(m.build());
        }
        setState((long) (st.optDouble("pos", 0) * 1000));
        show();
    }

    private void setState(long posMs) {
        long actions = PlaybackState.ACTION_PLAY_PAUSE | PlaybackState.ACTION_PLAY | PlaybackState.ACTION_PAUSE
                | PlaybackState.ACTION_SKIP_TO_NEXT | PlaybackState.ACTION_SKIP_TO_PREVIOUS;
        if (durMs > 0) actions |= PlaybackState.ACTION_SEEK_TO;
        PlaybackState.Builder b = new PlaybackState.Builder()
                .setActions(actions)
                .setState(playing ? PlaybackState.STATE_PLAYING : PlaybackState.STATE_PAUSED, posMs, playing ? 1f : 0f, SystemClock.elapsedRealtime());
        if (!title.isEmpty()) { // сердечко — всегда, пока играет трек
            boolean on = Boolean.TRUE.equals(liked);
            b.addCustomAction(new PlaybackState.CustomAction.Builder("like",
                    on ? "Убрать из «Мне нравится»" : "Мне нравится",
                    on ? R.drawable.ic_heart_on : R.drawable.ic_heart).build());
        }
        session.setPlaybackState(b.build());
    }

    // ---------- Уведомление ----------

    private PendingIntent action(String a, int req) {
        Intent i = new Intent(this, MusicService.class).setAction(a);
        return PendingIntent.getService(this, req, i, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);
    }

    private Notification.Action button(int icon, String label, String a, int req) {
        return new Notification.Action.Builder(Icon.createWithResource(this, icon), label, action(a, req)).build();
    }

    @SuppressWarnings("deprecation")
    private void show() {
        Notification.Builder b = Build.VERSION.SDK_INT >= 26 ? new Notification.Builder(this, CHANNEL) : new Notification.Builder(this);
        Intent open = new Intent(this, MainActivity.class).setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_REORDER_TO_FRONT);
        boolean none = title.isEmpty();
        b.setSmallIcon(R.drawable.ic_note)
                .setContentTitle(none ? "Музыка на компьютере" : title)
                .setContentText(none ? "Нажмите ▶, чтобы включить «Мою волну»" : artist)
                .setLargeIcon(cover)
                .setContentIntent(PendingIntent.getActivity(this, 0, open, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT))
                .setDeleteIntent(action("stop", 9))
                .setVisibility(Notification.VISIBILITY_PUBLIC)
                .setOnlyAlertOnce(true)
                .setShowWhen(false)
                .setOngoing(playing)
                // Кнопки для Android 12 и старше; в новых Android кнопки берутся из MediaSession
                .addAction(button(R.drawable.ic_prev, "Предыдущий", "prev", 1))
                .addAction(button(playing ? R.drawable.ic_pause : R.drawable.ic_play, playing ? "Пауза" : "Играть", "playpause", 2))
                .addAction(button(R.drawable.ic_next, "Следующий", "next", 3));
        if (!none) b.addAction(button(Boolean.TRUE.equals(liked) ? R.drawable.ic_heart_on : R.drawable.ic_heart, "Мне нравится", "like", 4));
        b.setStyle(new Notification.MediaStyle().setMediaSession(session.getSessionToken()).setShowActionsInCompactView(0, 1, 2));
        Notification n = b.build();
        if (!foreground) {
            try {
                if (Build.VERSION.SDK_INT >= 29) startForeground(NOTE_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK);
                else startForeground(NOTE_ID, n);
                foreground = true;
            } catch (Exception e) {
                nm.notify(NOTE_ID, n); // Android не разрешил службу на переднем плане — просто показываем
            }
        } else {
            nm.notify(NOTE_ID, n);
        }
        shown = true;
    }

    /** Ничего не играет: тихая карточка с кнопкой «плей» (включит «Мою волну» на компьютере). */
    private void idle() {
        boolean changed = !title.isEmpty() || playing || !shown;
        title = "";
        artist = "";
        playing = false;
        liked = null;
        if (!changed) return;
        cover = null;
        coverKey = "";
        durMs = 0;
        session.setMetadata(new MediaMetadata.Builder()
                .putString(MediaMetadata.METADATA_KEY_TITLE, "Музыка на компьютере")
                .putString(MediaMetadata.METADATA_KEY_ARTIST, "Нажмите ▶, чтобы включить «Мою волну»").build());
        setState(0);
        show();
    }

    // ---------- Команды ----------

    private void command(String a, Double pos) {
        // Сразу показываем результат, не дожидаясь компьютера
        if ("playpause".equals(a) && shown && !title.isEmpty()) {
            playing = !playing;
            setState(currentPos());
            show();
        } else if ("seek".equals(a) && pos != null) {
            setState((long) (pos * 1000));
        } else if ("like".equals(a) && shown && !title.isEmpty()) {
            liked = !Boolean.TRUE.equals(liked);
            setState(currentPos());
            show();
        }
        io.execute(() -> {
            try {
                JSONObject body = new JSONObject().put("action", a);
                if (pos != null) body.put("pos", pos);
                http("POST", base + "/r/api/media", body.toString());
            } catch (Exception ignored) {
            }
            main.post(() -> {
                if (destroyed) return;
                main.removeCallbacks(poll);
                main.postDelayed(poll, 500);
            });
        });
    }

    private long currentPos() {
        PlaybackState s = session.getController().getPlaybackState();
        if (s == null) return 0;
        long p = s.getPosition();
        if (s.getState() == PlaybackState.STATE_PLAYING) {
            p += (long) ((SystemClock.elapsedRealtime() - s.getLastPositionUpdateTime()) * s.getPlaybackSpeed());
        }
        return p;
    }

    // ---------- Сеть ----------

    private String http(String method, String url, String body) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setConnectTimeout(4000);
        c.setReadTimeout(6000);
        c.setRequestMethod(method);
        c.setRequestProperty("X-Remote", key);
        if (body != null) {
            c.setDoOutput(true);
            c.setRequestProperty("Content-Type", "application/json");
            try (OutputStream o = c.getOutputStream()) { o.write(body.getBytes("UTF-8")); }
        }
        try (InputStream in = c.getInputStream()) {
            return new String(readAll(in), "UTF-8");
        } finally {
            c.disconnect();
        }
    }

    private static byte[] readAll(InputStream in) throws Exception {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        byte[] buf = new byte[16384];
        for (int n; (n = in.read(buf)) > 0; ) out.write(buf, 0, n);
        return out.toByteArray();
    }

    /** Обложка: с компьютера или по ссылке. Загружаем заново только когда она сменилась. */
    private Bitmap loadCover(JSONObject st) {
        String ver = st.optString("coverVer", ""), link = st.optString("coverUrl", "");
        String k = !ver.isEmpty() ? "v:" + ver : link;
        if (k.equals(coverKey)) return cover;
        Bitmap b = null;
        try {
            String url = !ver.isEmpty() ? base + "/r/api/media/cover?k=" + Uri.encode(key) + "&v=" + Uri.encode(ver)
                    : link.startsWith("//") ? "https:" + link : link;
            if (!url.isEmpty()) {
                HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
                c.setConnectTimeout(4000);
                c.setReadTimeout(8000);
                try (InputStream in = c.getInputStream()) {
                    byte[] data = readAll(in);
                    BitmapFactory.Options o = new BitmapFactory.Options();
                    o.inJustDecodeBounds = true;
                    BitmapFactory.decodeByteArray(data, 0, data.length, o);
                    o.inSampleSize = 1;
                    while (Math.max(o.outWidth, o.outHeight) / o.inSampleSize > 640) o.inSampleSize *= 2;
                    o.inJustDecodeBounds = false;
                    b = BitmapFactory.decodeByteArray(data, 0, data.length, o);
                } finally {
                    c.disconnect();
                }
            }
        } catch (Exception ignored) {
        }
        coverKey = k;
        return b;
    }
}

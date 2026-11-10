package com.jenew.zroksmoke;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.os.IBinder;
import java.util.concurrent.atomic.AtomicBoolean;

public final class ZrokForegroundService extends Service {
    private static final String CHANNEL_ID = "jenew_zrok_tunnels";
    private static final int NOTIFICATION_ID = 7402;
    private static final AtomicBoolean RESTORE_IN_PROGRESS = new AtomicBoolean(false);
    private static final String PREFS = "jenew_zrok_runtime";

    @Override public void onCreate() {
        super.onCreate();
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel channel = new NotificationChannel(
                CHANNEL_ID, "JENEW zrok tunnels", NotificationManager.IMPORTANCE_LOW);
            channel.setDescription("Keeps active zrok share and access tunnels running");
            NotificationManager manager = getSystemService(NotificationManager.class);
            if (manager != null) manager.createNotificationChannel(channel);
        }
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        Notification.Builder builder = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
            ? new Notification.Builder(this, CHANNEL_ID)
            : new Notification.Builder(this);
        Notification notification = builder
            .setSmallIcon(android.R.drawable.stat_sys_upload_done)
            .setContentTitle("JENEW zrok service")
            .setContentText("Starting or restoring tunnel engines…")
            .setOngoing(true)
            .setCategory(Notification.CATEGORY_SERVICE)
            .build();
        startForeground(NOTIFICATION_ID, notification);
        if (RESTORE_IN_PROGRESS.compareAndSet(false, true)) {
            new Thread(() -> {
                try {
                    go.Seq.setContext(getApplicationContext());
                    bridge.Bridge.initialize(getFilesDir().getAbsolutePath() + "/zrok2");
                    if (bridge.Bridge.activeBackendCount() == 0) bridge.Bridge.restoreManagedBackends();
                    getSharedPreferences(PREFS, MODE_PRIVATE).edit().remove("restoreError")
                        .putLong("restoreCompletedAt", System.currentTimeMillis()).apply();
                    NotificationManager manager = getSystemService(NotificationManager.class);
                    if (manager != null) {
                        Notification active = builder.setContentTitle("JENEW zrok service")
                            .setContentText(bridge.Bridge.activeBackendCount() + " tunnel engine(s) active")
                            .setOngoing(true).setCategory(Notification.CATEGORY_SERVICE).build();
                        manager.notify(NOTIFICATION_ID, active);
                    }
                } catch (Throwable failure) {
                    String message = failure.getMessage();
                    if (message == null || message.trim().isEmpty()) message = failure.getClass().getSimpleName();
                    getSharedPreferences(PREFS, MODE_PRIVATE).edit().putString("restoreError", message).apply();
                } finally {
                    RESTORE_IN_PROGRESS.set(false);
                }
            }, "JENEW-zrok-restore").start();
        }
        return START_STICKY;
    }

    @Override public IBinder onBind(Intent intent) { return null; }

    public static void start(Context context) {
        Intent intent = new Intent(context, ZrokForegroundService.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) context.startForegroundService(intent);
        else context.startService(intent);
    }

    public static void stop(Context context) {
        context.stopService(new Intent(context, ZrokForegroundService.class));
    }
}

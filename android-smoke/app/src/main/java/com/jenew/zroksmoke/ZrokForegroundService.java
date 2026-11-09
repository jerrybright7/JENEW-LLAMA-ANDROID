package com.jenew.zroksmoke;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.os.IBinder;

public final class ZrokForegroundService extends Service {
    private static final String CHANNEL_ID = "jenew_zrok_tunnels";
    private static final int NOTIFICATION_ID = 7402;

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
            .setContentTitle("JENEW zrok is active")
            .setContentText("Your selected zrok share/access engines are running")
            .setOngoing(true)
            .setCategory(Notification.CATEGORY_SERVICE)
            .build();
        startForeground(NOTIFICATION_ID, notification);
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

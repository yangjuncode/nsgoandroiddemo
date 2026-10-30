package dev.oneo.nsgo.servicedemo;

import android.app.Activity;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Intent;
import android.content.MutableContextWrapper;
import android.content.pm.ServiceInfo;
import android.graphics.Bitmap;
import android.graphics.BitmapFactory;
import android.graphics.drawable.Icon;
import android.os.Build;
import android.os.IBinder;
import android.util.Log;
import android.view.ViewGroup;
import android.view.ViewParent;

import dev.oneo.nsgo.NsgoView;

/**
 * 外层独立 Android 宿主工程的前台常驻服务 (ForegroundGoService)：
 * 1. 启动前台通知栏并挂载自定义图标 (R.mipmap.ic_launcher)；
 * 2. 通过 MutableContextWrapper 托管常驻 NsgoView，当 MainActivity 关闭 (onDestroy) 时不销毁 Go 进程；
 * 3. 通过 NsgoView 官方宿主桥 (send / handle) 通知 Go Service 感知 Activity 的打开与关闭，并将 Go 日志输出到 Logcat。
 */
public class ForegroundGoService extends Service {
    public static final String TAG = "NSGoService";
    private static final String CHANNEL_ID = "nsgo_fg_service_channel";
    private static final int NOTIFICATION_ID = 1001;

    private static ForegroundGoService instance;
    private static MutableContextWrapper viewContext;
    private static NsgoView sharedView;
    private static MainActivity attachedActivity;
    private static String lastStatusText = "后台 Go Service 正在运行中 (点击返回应用)";

    public static synchronized NsgoView attachActivity(MainActivity activity) {
        Intent serviceIntent = new Intent(activity, ForegroundGoService.class);
        try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                activity.startForegroundService(serviceIntent);
            } else {
                activity.startService(serviceIntent);
            }
        } catch (Throwable e) {
            Log.w(TAG, "Failed to start foreground service: " + e);
        }

        attachedActivity = activity;
        if (sharedView == null) {
            viewContext = new MutableContextWrapper(activity);
            sharedView = new NsgoView(viewContext);

            // 处理来自 Go UI 的关闭 Activity 请求
            sharedView.handle("finish_activity", (data, reply) -> {
                Activity a = attachedActivity;
                Log.i(TAG, "[Android Host] 收到 Go UI 请求关闭 Activity (finish_activity)");
                if (a != null) {
                    a.finish();
                }
                reply.resolve(true);
            });

            // 处理来自 Go 后台 Service 的实时日志与前台通知栏文字更新
            sharedView.handle("service_log", (data, reply) -> {
                if (data != null) {
                    String msg = String.valueOf(data);
                    Log.i(TAG, msg);
                    updateNotificationText(msg);
                }
                reply.resolve(true);
            });

            Log.i(TAG, "[Android Host] 首次创建 NsgoView，启动 Go 引擎与后台常驻 Service");
        } else {
            ViewParent parent = sharedView.getParent();
            if (parent instanceof ViewGroup) {
                ((ViewGroup) parent).removeView(sharedView);
            }
            viewContext.setBaseContext(activity);
            Log.i(TAG, "[Android Host] MainActivity 重新打开 -> 复用常驻 Go 进程并重新挂载 UI 视图");
            sharedView.send("activity_lifecycle", "attached");
        }
        return sharedView;
    }

    public static synchronized void detachActivity(MainActivity activity) {
        if (sharedView != null) {
            ViewParent parent = sharedView.getParent();
            if (parent instanceof ViewGroup) {
                ((ViewGroup) parent).removeView(sharedView);
            }
        }
        if (attachedActivity == activity) {
            attachedActivity = null;
            if (viewContext != null) {
                // 切回 ApplicationContext，防止已销毁的 MainActivity 内存泄漏
                viewContext.setBaseContext(activity.getApplicationContext());
            }
            if (sharedView != null) {
                // 通知 Go 后台服务：UI Activity 已被关闭
                sharedView.send("activity_lifecycle", "detached");
            }
            Log.i(TAG, "[Android Host] MainActivity onDestroy -> UI Activity 已关闭，前台 Service 与 Go 进程继续在后台存活");
            updateNotificationText("UI Activity 已关闭 · Go Service 在后台独立运行中");
        }
    }

    public static synchronized void updateNotificationText(String text) {
        if (text == null || text.isEmpty()) return;
        lastStatusText = text;
        if (instance != null) {
            NotificationManager nm = instance.getSystemService(NotificationManager.class);
            if (nm != null) {
                nm.notify(NOTIFICATION_ID, instance.buildNotification(lastStatusText));
            }
        }
    }

    @Override
    public void onCreate() {
        super.onCreate();
        instance = this;
        createChannel();
        startForegroundCompat(buildNotification(lastStatusText));
        Log.i(TAG, "[Android Host] ForegroundGoService onCreate -> 前台服务已启动并挂载状态栏图标");
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        instance = this;
        createChannel();
        startForegroundCompat(buildNotification(lastStatusText));
        return START_STICKY;
    }

    private void startForegroundCompat(Notification notification) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        } else {
            startForeground(NOTIFICATION_ID, notification);
        }
    }

    private void createChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationManager nm = getSystemService(NotificationManager.class);
            if (nm == null) return;
            NotificationChannel channel = new NotificationChannel(
                    CHANNEL_ID,
                    "NSGo 前台常驻服务",
                    NotificationManager.IMPORTANCE_LOW);
            channel.setDescription("保持 NSGo 后台 Go Service 常驻运行");
            channel.setShowBadge(true);
            nm.createNotificationChannel(channel);
        }
    }

    private Notification buildNotification(String contentText) {
        int iconRes = R.mipmap.ic_launcher;
        Intent launchIntent = new Intent(this, MainActivity.class);
        launchIntent.setFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP | Intent.FLAG_ACTIVITY_CLEAR_TOP);
        PendingIntent pendingIntent = PendingIntent.getActivity(
                this, 0, launchIntent, PendingIntent.FLAG_IMMUTABLE | PendingIntent.FLAG_UPDATE_CURRENT);

        Notification.Builder builder = new Notification.Builder(this, CHANNEL_ID)
                .setSmallIcon(Icon.createWithResource(this, iconRes))
                .setContentTitle("NSGo 前台常驻 Go Service")
                .setContentText(contentText)
                .setContentIntent(pendingIntent)
                .setOngoing(true)
                .setCategory(Notification.CATEGORY_SERVICE);

        try {
            Bitmap large = BitmapFactory.decodeResource(getResources(), iconRes);
            if (large != null) {
                builder.setLargeIcon(large);
            }
        } catch (Throwable ignored) {
        }
        return builder.build();
    }

    @Override
    public void onTaskRemoved(Intent rootIntent) {
        super.onTaskRemoved(rootIntent);
        Log.i(TAG, "[Android Host] onTaskRemoved -> 最近任务卡片被移除，前台常驻 Service 继续在后台运行");
    }

    @Override
    public void onDestroy() {
        if (instance == this) {
            instance = null;
        }
        Log.i(TAG, "[Android Host] ForegroundGoService onDestroy");
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}

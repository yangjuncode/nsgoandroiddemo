package dev.oneo.nsgo.servicedemo;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.content.res.Configuration;
import android.graphics.Color;
import android.os.Build;
import android.os.Bundle;
import android.view.Window;
import android.view.WindowManager;

import dev.oneo.nsgo.NsgoView;

/**
 * 外层独立 Android 宿主工程的 MainActivity：
 * - onCreate 时启动 ForegroundGoService 并挂载常驻的 NsgoView；
 * - onDestroy 时仅解绑 NsgoView 视图并通知 Go Service 感知 UI 关闭，不销毁后台 Go 进程。
 */
public class MainActivity extends Activity {
    private NsgoView view;

    @Override
    protected void onCreate(Bundle state) {
        int night = getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK;
        if (night != Configuration.UI_MODE_NIGHT_YES) {
            setTheme(android.R.style.Theme_DeviceDefault_Light_NoActionBar);
        }
        super.onCreate(state);

        Window window = getWindow();
        WindowManager.LayoutParams lp = window.getAttributes();
        lp.layoutInDisplayCutoutMode = WindowManager.LayoutParams.LAYOUT_IN_DISPLAY_CUTOUT_MODE_SHORT_EDGES;
        window.setAttributes(lp);
        window.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        window.setStatusBarColor(Color.TRANSPARENT);
        window.setNavigationBarColor(Color.TRANSPARENT);
        if (Build.VERSION.SDK_INT >= 30) {
            window.setDecorFitsSystemWindows(false);
        }

        if (Build.VERSION.SDK_INT >= 33) {
            if (checkSelfPermission("android.permission.POST_NOTIFICATIONS") != PackageManager.PERMISSION_GRANTED) {
                requestPermissions(new String[] {"android.permission.POST_NOTIFICATIONS"}, 100);
            }
        }

        view = ForegroundGoService.attachActivity(this);
        setContentView(view);
    }

    @Override
    public void onBackPressed() {
        finish();
    }

    @Override
    protected void onActivityResult(int request, int result, Intent data) {
        super.onActivityResult(request, result, data);
        if (view != null) {
            view.activityResult(request, result, data);
        }
    }

    @Override
    protected void onDestroy() {
        ForegroundGoService.detachActivity(this);
        view = null;
        super.onDestroy();
    }
}
